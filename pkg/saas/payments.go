package saas

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/saas/docusage"
	"tukifac/pkg/saas/pricing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SubmitPaymentInput pago enviado por tenant o admin.
type SubmitPaymentInput struct {
	TenantID       uint
	BillingCycleID uint
	Amount         float64
	PaymentMethod  string
	PaymentDate    *time.Time
	Reference      string
	ReceiptURL     string
	Notes          string
	SubmittedBy    *uint
	FromAdmin      bool
	PeriodMonths   int
	PlanID         uint
}

// SubmitPayment registra pago; provisional máx 12h y 1 vez por ciclo.
func SubmitPayment(in SubmitPaymentInput) (*database.SaasPayment, error) {
	if in.TenantID == 0 {
		return nil, errors.New("tenant_id requerido")
	}

	var payment *database.SaasPayment
	err := database.CentralDB.Transaction(func(tx *gorm.DB) error {
		var tenant database.Tenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&tenant, in.TenantID).Error; err != nil {
			return err
		}
		if !in.FromAdmin {
			if err := CanTenantSubmitPayment(&tenant); err != nil {
				return err
			}
		}

		cfg, _ := LoadSettings()
		var cycle *database.SaasBillingCycle
		if in.BillingCycleID > 0 {
			var c database.SaasBillingCycle
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, in.BillingCycleID).Error; err != nil {
				return errors.New("ciclo de facturación no encontrado")
			}
			if c.TenantID != in.TenantID {
				return errors.New("ciclo no pertenece al tenant")
			}
			cycle = &c
		}

		var sub database.SaasSubscription
		hasSub := tx.Where("tenant_id = ?", in.TenantID).Order("created_at desc").First(&sub).Error == nil
		if cycle != nil && in.Amount <= 0 {
			var subPtr *database.SaasSubscription
			if hasSub {
				subPtr = &sub
			}
			in.Amount = BillingCycleAmountDue(cycle, &tenant, subPtr)
		}

		// Si el tenant vuelve a subir un comprobante para el mismo ciclo (p. ej. el primero
		// nunca se revisó), cerrar los pending_review/pending anteriores de ese ciclo: sin
		// esto se apilaban comprobantes sin resolver y el tenant seguía viendo "pago en
		// revisión" para siempre aunque ya hubiera vuelto a pagar.
		if cycle != nil {
			if err := supersedePriorPendingPayments(tx, cycle.ID, NowLima()); err != nil {
				return err
			}
		}

		status := database.SaasPayPendingReview
		if in.FromAdmin {
			status = database.SaasPayPending
		}

		p := &database.SaasPayment{
			TenantID: in.TenantID, Amount: in.Amount, Currency: "PEN",
			PeriodMonths: in.PeriodMonths, PaymentMethod: in.PaymentMethod,
			PaymentDate: in.PaymentDate, Reference: in.Reference,
			ReceiptURL: in.ReceiptURL, Notes: in.Notes, Status: status, SubmittedBy: in.SubmittedBy,
		}
		if cycle != nil {
			p.BillingCycleID = &cycle.ID
			var subPtr *database.SaasSubscription
			if hasSub {
				subPtr = &sub
			}
			if ChargeReconnectionFee(&tenant, subPtr) {
				p.ReconnectionFee = cycle.ReconnectionFee
			}
		}

		if hasSub {
			p.SubscriptionID = &sub.ID
		}

		if err := tx.Create(p).Error; err != nil {
			return err
		}
		payment = p

		if !in.FromAdmin && cfg.ProvisionalReactivationEnabled && cycle != nil && !cycle.ProvisionalUsed {
			needsProvisional := sub.Status == database.SaasSubSuspended ||
				sub.Status == database.SaasSubOverdue ||
				tenant.Status == database.TenantStatusSuspended
			if needsProvisional {
				until := NowLima().Add(EffectiveProvisionalHours(cfg.ProvisionalHours))
				if err := tx.Model(&sub).Updates(map[string]interface{}{
					"status":            database.SaasSubProvisionalActive,
					"provisional_until": until,
				}).Error; err != nil {
					return err
				}
				if err := tx.Model(cycle).Update("provisional_used", true).Error; err != nil {
					return err
				}
				if err := tx.Model(&tenant).Update("status", database.TenantStatusActive).Error; err != nil {
					return err
				}
				if err := tx.Model(p).Update("provisional_applied", true).Error; err != nil {
					return err
				}
				sid := sub.ID
				LogEventTx(tx, in.TenantID, &sid, EventProvisionalGranted, "tenant", in.SubmittedBy,
					"reactivación provisional", MetaJSON(map[string]interface{}{
						"payment_id": p.ID, "until": until.Format(time.RFC3339), "billing_cycle_id": cycle.ID,
					}))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	InvalidateTenantCache(in.TenantID)
	QueueNotification(in.TenantID, 0, "email", "payment_received", map[string]interface{}{
		"payment_id": payment.ID,
	})
	return payment, nil
}

// ApprovePayment transacción segura: aprueba, extiende la suscripción HASTA EL PERÍODO PAGADO,
// exige que el monto cubra la deuda, marca el ciclo pagado y limpia strikes.
//
// Modelo prepago: pagar un ciclo extiende la suscripción exactamente hasta `cycle.period_end`
// (una deuda de 6 meses da +6 meses, no +1 del plan). La extensión es EN SITIO (no crea una
// suscripción nueva ni una deuda fantasma del período recién pagado); el ciclo pagado queda
// ligado a la suscripción y sirve de cupo de documentos del período.
func ApprovePayment(paymentID uint, planID uint, periodMonths int, adminNotes string, reviewerID uint) error {
	return database.CentralDB.Transaction(func(tx *gorm.DB) error {
		var payment database.SaasPayment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&payment, paymentID).Error; err != nil {
			return errors.New("pago no encontrado")
		}
		if payment.Status != database.SaasPayPendingReview && payment.Status != database.SaasPayPending {
			return fmt.Errorf("el pago ya fue %s", payment.Status)
		}

		var tenant database.Tenant
		_ = tx.First(&tenant, payment.TenantID).Error
		var curSub database.SaasSubscription
		hasCurSub := tx.Where("tenant_id = ?", payment.TenantID).
			Where("status NOT IN ?", []string{database.SaasSubCancelled}).
			Order("created_at desc").First(&curSub).Error == nil

		// Resolver el ciclo que se paga: el vinculado, o el pendiente vigente del tenant (evita
		// que un pago externo del superadmin sin billing_cycle_id deje la deuda viva).
		var cycle *database.SaasBillingCycle
		if payment.BillingCycleID != nil {
			var c database.SaasBillingCycle
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, *payment.BillingCycleID).Error; err != nil {
				return errors.New("ciclo de facturación no encontrado")
			}
			cycle = &c
		} else {
			var c database.SaasBillingCycle
			if tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("tenant_id = ? AND status = ?", payment.TenantID, database.SaasInvoicePending).
				Order("period_end asc").First(&c).Error == nil {
				cycle = &c
				_ = tx.Model(&payment).Update("billing_cycle_id", c.ID).Error
				payment.BillingCycleID = &c.ID
			}
		}
		if cycle != nil {
			if err := guardBillingCycleApprove(tx, cycle.ID, payment.ID); err != nil {
				return err
			}
			// Conciliación de monto: exigir pago completo (tolerancia de 1 céntimo).
			var subPtr *database.SaasSubscription
			if hasCurSub {
				subPtr = &curSub
			}
			due := BillingCycleAmountDue(cycle, &tenant, subPtr)
			if payment.Amount+0.009 < due {
				return fmt.Errorf("el pago (S/ %.2f) no cubre la deuda (S/ %.2f); registra un pago que cubra el total", payment.Amount, due)
			}
		}

		now := NowLima()
		if err := tx.Model(&payment).Updates(map[string]interface{}{
			"status": database.SaasPayApproved, "admin_notes": adminNotes,
			"reviewed_by": reviewerID, "reviewed_at": now,
		}).Error; err != nil {
			return err
		}

		if planID == 0 {
			if cycle != nil && cycle.PlanID > 0 {
				planID = cycle.PlanID
			} else if hasCurSub {
				planID = curSub.PlanID
			}
		}

		var sub *database.SaasSubscription
		var err error
		if cycle != nil {
			// Extensión EN SITIO hasta el fin del período pagado (sin deuda fantasma).
			sub, err = extendSubscriptionToCycleTx(tx, payment.TenantID, planID, cycle,
				fmt.Sprintf("Pago #%d aprobado", paymentID))
		} else {
			// Edge: pago sin ningún ciclo → extensión clásica por meses del plan.
			extendMonths := periodMonths
			if extendMonths <= 0 {
				extendMonths = payment.PeriodMonths
			}
			if extendMonths <= 0 && hasCurSub && curSub.BillingCycle != "" {
				extendMonths = CycleMonthsFromBilling(curSub.BillingCycle)
			}
			sub, err = extendSubscriptionTx(tx, payment.TenantID, planID, extendMonths,
				fmt.Sprintf("Pago #%d aprobado", paymentID))
		}
		if err != nil {
			return err
		}

		if err := tx.Model(&payment).Update("subscription_id", sub.ID).Error; err != nil {
			return err
		}
		if cycle != nil {
			_ = markCyclePaidTx(tx, cycle.ID, payment.ID)
			// Si el mismo ciclo tenía otro(s) pago(s) que quedaron pending_review sin
			// resolver (p. ej. un comprobante viejo que nadie aprobó/rechazó y luego el
			// tenant volvió a pagar), quedaban colgados para siempre: guardBillingCycleApprove
			// solo evita aprobar dos pagos por ciclo, pero no cierra los que ya no aplican.
			// Eso hacía que /subscription siguiera mostrando "pago en revisión" indefinidamente
			// aunque el ciclo ya estuviera pagado. Se superan automáticamente al aprobar éste.
			if err := supersedeSiblingPendingPayments(tx, cycle.ID, payment.ID, reviewerID, now); err != nil {
				return err
			}
		}

		sid := sub.ID
		if err := ClearStrikesOnApprove(tx, payment.TenantID, &sid, &reviewerID); err != nil {
			return err
		}
		LogEventTx(tx, payment.TenantID, &sid, EventPaymentApproved, "admin", &reviewerID, adminNotes, "")
		LogEventTx(tx, payment.TenantID, &sid, EventReactivated, "admin", &reviewerID, adminNotes, "")
		return nil
	})
}

// extendSubscriptionToCycleTx extiende EN SITIO la suscripción hasta el fin del período del ciclo
// pagado (prepago). No crea suscripción ni ciclo nuevos: el ciclo pagado queda como período
// vigente (y contenedor del cupo de documentos). Garantiza una sola suscripción activa.
func extendSubscriptionToCycleTx(tx *gorm.DB, tenantID, planID uint, cycle *database.SaasBillingCycle, notes string) (*database.SaasSubscription, error) {
	if tenantID == 0 || planID == 0 {
		return nil, errors.New("tenant_id y plan_id requeridos")
	}
	var plan database.SaasPlan
	if err := tx.First(&plan, planID).Error; err != nil {
		return nil, errors.New("plan no encontrado")
	}
	billing := plan.BillingCycle
	if billing == "" {
		billing = database.SaasCycleMonthly
	}

	// Suscripción a extender: la del ciclo si existe; si no, la vigente del tenant.
	var sub database.SaasSubscription
	found := false
	if cycle.SubscriptionID > 0 {
		found = tx.First(&sub, cycle.SubscriptionID).Error == nil
	}
	if !found {
		found = tx.Where("tenant_id = ?", tenantID).Order("created_at desc").First(&sub).Error == nil
	}
	if !found {
		return nil, errors.New("suscripción no encontrada para el ciclo")
	}

	// billed_months pasa a ser el del período que se acaba de pagar: si conservara el del alta,
	// un cobro futuro para esta suscripción se calcularía con una duración que ya no aplica.
	billedMonths := cycle.MonthsCovered
	if billedMonths <= 0 {
		billedMonths = pricing.MonthsBetween(cycle.PeriodStart, cycle.PeriodEnd, lima())
	}
	if err := tx.Model(&sub).Updates(map[string]interface{}{
		"plan_id":           planID,
		"billing_cycle":     billing,
		"end_date":          EndOfDayLima(cycle.PeriodEnd),
		"status":            database.SaasSubActive,
		"billed_months":     billedMonths,
		"provisional_until": nil,
		"grace_ends_at":     nil,
		"notes":             notes,
	}).Error; err != nil {
		return nil, err
	}
	// Una sola suscripción activa: el resto (no canceladas) pasa a expired.
	_ = tx.Model(&database.SaasSubscription{}).
		Where("tenant_id = ? AND id <> ? AND status NOT IN ?", tenantID, sub.ID, []string{database.SaasSubCancelled}).
		Update("status", database.SaasSubExpired)

	syncTenantModulesFromPlanTx(tx, tenantID, planID)
	_ = tx.Model(&database.Tenant{}).Where("id = ?", tenantID).
		Updates(map[string]interface{}{"plan": plan.Name, "status": database.TenantStatusActive}).Error
	// El ciclo pagado debe pertenecer a esta suscripción (para el cupo de documentos).
	if cycle.SubscriptionID != sub.ID {
		_ = tx.Model(&database.SaasBillingCycle{}).Where("id = ?", cycle.ID).Update("subscription_id", sub.ID).Error
	}
	_ = tx.First(&sub, sub.ID).Error
	return &sub, nil
}

// RejectPayment transacción segura: rechaza, revierte provisional, aplica strikes.
func RejectPayment(paymentID uint, adminNotes string, reviewerID uint) error {
	var tenantID uint
	err := database.CentralDB.Transaction(func(tx *gorm.DB) error {
		var payment database.SaasPayment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&payment, paymentID).Error; err != nil {
			return errors.New("pago no encontrado")
		}
		if payment.Status != database.SaasPayPendingReview && payment.Status != database.SaasPayPending {
			return fmt.Errorf("el pago ya fue %s", payment.Status)
		}
		tenantID = payment.TenantID
		now := NowLima()
		if err := tx.Model(&payment).Updates(map[string]interface{}{
			"status": database.SaasPayRejected, "admin_notes": adminNotes,
			"reviewed_by": reviewerID, "reviewed_at": now,
		}).Error; err != nil {
			return err
		}

		// Si el ciclo de este pago YA quedó pagado (por otro pago aprobado), rechazar este
		// es limpieza administrativa de un comprobante duplicado/sobrante — no la constatación
		// de un comprobante inválido o fraudulento. Penalizar al tenant aquí (strike, suspender
		// tenant/suscripción) lo castigaba por algo que ya había pagado correctamente.
		if isDuplicateOfPaidCycle(tx, payment.BillingCycleID, payment.ID) {
			return nil
		}

		var subID *uint
		if payment.SubscriptionID != nil {
			subID = payment.SubscriptionID
		}
		if payment.ProvisionalApplied && subID != nil {
			_ = tx.Model(&database.SaasSubscription{}).Where("id = ?", *subID).Updates(map[string]interface{}{
				"status": database.SaasSubSuspended, "provisional_until": nil,
			})
		}

		_, blocked, err := ApplyStrikeOnReject(tx, payment.TenantID, subID, &reviewerID, adminNotes)
		if err != nil {
			return err
		}
		if blocked {
			QueueNotification(payment.TenantID, 0, "in_app", "tenant_blocked", map[string]interface{}{"reason": adminNotes})
		}
		return nil
	})
	if err != nil {
		return err
	}
	InvalidateTenantCache(tenantID)
	return nil
}

// isDuplicateOfPaidCycle true si el pago pertenece a un ciclo de facturación que ya quedó
// pagado (directamente, o vía otro pago aprobado) — es decir, es un comprobante sobrante y
// no uno inválido. Si el pago no está ligado a ningún ciclo (billingCycleID nil), no hay forma
// de saberlo y se trata como un rechazo normal (con strike), igual que antes de este fix.
func isDuplicateOfPaidCycle(tx *gorm.DB, billingCycleID *uint, paymentID uint) bool {
	if billingCycleID == nil {
		return false
	}
	var cycle database.SaasBillingCycle
	if err := tx.First(&cycle, *billingCycleID).Error; err != nil {
		return false
	}
	if cycle.Status == database.SaasInvoicePaid {
		return true
	}
	var approvedCount int64
	tx.Model(&database.SaasPayment{}).
		Where("billing_cycle_id = ? AND status = ? AND id <> ?", *billingCycleID, database.SaasPayApproved, paymentID).
		Count(&approvedCount)
	return approvedCount > 0
}

func extendSubscriptionTx(tx *gorm.DB, tenantID uint, planID uint, months int, notes string, discount ...Discount) (*database.SaasSubscription, error) {
	var d Discount
	if len(discount) > 0 {
		d = discount[0]
	}
	if tenantID == 0 || planID == 0 {
		return nil, errors.New("tenant_id y plan_id requeridos")
	}
	var plan database.SaasPlan
	if err := tx.First(&plan, planID).Error; err != nil {
		return nil, errors.New("plan no encontrado")
	}
	cycle := plan.BillingCycle
	if cycle == "" {
		cycle = database.SaasCycleMonthly
	}
	if months <= 0 {
		months = CycleMonthsFromBilling(cycle)
	}

	// Renovar el MISMO plan extiende la suscripción vigente en lugar de crear otra.
	//
	// Crear una fila nueva dejaba el cobro impago de la anterior suelto y solapado con el
	// nuevo período (mismos días facturados dos veces), y llenaba la lista del panel de
	// «históricas» en cada renovación. Una fila nueva solo tiene sentido cuando cambia el
	// plan, que es cuando de verdad empieza otro contrato.
	if current, ok := currentSubscriptionForRenewalTx(tx, tenantID); ok && current.PlanID == planID {
		return renewInPlaceTx(tx, current, &plan, cycle, months, notes, d)
	}

	_ = tx.Model(&database.SaasSubscription{}).
		Where("tenant_id = ? AND status NOT IN ?", tenantID, []string{database.SaasSubCancelled}).
		Update("status", database.SaasSubExpired)

	now := NowLima()
	var prev database.SaasSubscription
	base := CalendarDateLima(now)
	if err := tx.Where("tenant_id = ?", tenantID).Order("end_date desc").First(&prev).Error; err == nil {
		prevDay := CalendarDateLima(prev.EndDate)
		if prevDay.After(base) {
			base = prevDay
		}
	}
	endDay := base.AddDate(0, months, 0)

	sub := &database.SaasSubscription{
		TenantID: tenantID, PlanID: planID, BillingCycle: cycle,
		StartDate: now, EndDate: EndOfDayLima(endDay),
		Status: database.SaasSubActive, Notes: notes,
		BilledMonths: months, DiscountType: d.Type, DiscountValue: d.Value,
	}
	if err := tx.Create(sub).Error; err != nil {
		return nil, err
	}
	syncTenantModulesFromPlanTx(tx, tenantID, planID)
	_ = tx.Model(&database.Tenant{}).Where("id = ?", tenantID).
		Updates(map[string]interface{}{"plan": plan.Name, "status": database.TenantStatusActive}).Error
	if _, err := ensureBillingCycleTx(tx, sub); err != nil {
		return nil, fmt.Errorf("ciclo de facturación: %w", err)
	}
	if err := voidOverlappingUnpaidCyclesTx(tx, tenantID, sub.ID, sub.StartDate, sub.EndDate); err != nil {
		return nil, fmt.Errorf("anulando cobros solapados: %w", err)
	}
	return sub, nil
}

// voidOverlappingUnpaidCyclesTx anula los cobros impagos que el período nuevo vuelve a cubrir.
//
// Cuando se abre otro contrato (cambio de plan) los cobros impagos de la suscripción anterior
// quedan vivos y, como el período nuevo arranca hoy mientras el viejo seguía corriendo, ambos
// facturan los mismos días. Cobrar los dos sería duplicar, no reclamar deuda.
//
// El corte es por SOLAPAMIENTO, no por antigüedad: un cobro cuyo período ya cerró antes de que
// empiece el nuevo sí es deuda real por servicio prestado y se conserva para poder cobrarlo.
func voidOverlappingUnpaidCyclesTx(
	tx *gorm.DB,
	tenantID uint,
	keepSubscriptionID uint,
	newStart, newEnd time.Time,
) error {
	var cycles []database.SaasBillingCycle
	if err := tx.Where("tenant_id = ? AND subscription_id <> ? AND status IN ?",
		tenantID, keepSubscriptionID,
		[]string{database.SaasInvoicePending, database.SaasInvoiceOverdue}).
		Find(&cycles).Error; err != nil {
		return err
	}
	for i := range cycles {
		c := &cycles[i]
		// Intervalos [inicio, fin) que se cruzan en al menos un día.
		if !c.PeriodStart.Before(newEnd) || !newStart.Before(c.PeriodEnd) {
			continue
		}
		if err := tx.Model(c).Update("status", database.SaasInvoiceRejected).Error; err != nil {
			return err
		}
		sid := keepSubscriptionID
		LogEventTx(tx, tenantID, &sid, EventInvoiceSuperseded, "system", nil,
			"cobro anulado: su período quedó cubierto por la nueva suscripción",
			MetaJSON(map[string]interface{}{
				"billing_cycle_id":  c.ID,
				"amount":            c.Amount,
				"period_start":      CalendarDateLima(c.PeriodStart).Format("2006-01-02"),
				"period_end":        CalendarDateLima(c.PeriodEnd).Format("2006-01-02"),
				"superseded_by_sub": keepSubscriptionID,
			}))
	}
	return nil
}

// currentSubscriptionForRenewalTx suscripción vigente del tenant (la última no cancelada),
// que es la misma regla con la que el runtime decide qué suscripción gobierna.
func currentSubscriptionForRenewalTx(tx *gorm.DB, tenantID uint) (*database.SaasSubscription, bool) {
	var sub database.SaasSubscription
	err := tx.Where("tenant_id = ?", tenantID).
		Where("status NOT IN ?", []string{database.SaasSubCancelled}).
		Order("created_at desc").First(&sub).Error
	if err != nil {
		return nil, false
	}
	return &sub, true
}

// renewInPlaceTx alarga la suscripción vigente y emite el cobro del tramo añadido.
//
// El período nuevo arranca donde termina lo ya cubierto —vigencia actual o el último cobro
// emitido, lo que llegue más lejos—, así las renovaciones quedan encadenadas sin solapar días
// con cobros anteriores. Si la suscripción ya venció, arranca hoy: no se regalan días pasados.
func renewInPlaceTx(
	tx *gorm.DB,
	sub *database.SaasSubscription,
	plan *database.SaasPlan,
	billingCycle string,
	months int,
	notes string,
	d Discount,
) (*database.SaasSubscription, error) {
	base := CalendarDateLima(NowLima())
	if end := CalendarDateLima(sub.EndDate); end.After(base) {
		base = end
	}
	var lastCovered database.SaasBillingCycle
	if err := tx.Where("subscription_id = ? AND status NOT IN ?", sub.ID,
		[]string{database.SaasInvoiceRejected}).
		Order("period_end desc").First(&lastCovered).Error; err == nil {
		if d := CalendarDateLima(lastCovered.PeriodEnd); d.After(base) {
			base = d
		}
	}

	periodStart := EndOfDayLima(base)
	newEnd := EndOfDayLima(base.AddDate(0, months, 0))

	if err := tx.Model(sub).Updates(map[string]interface{}{
		"plan_id":           plan.ID,
		"billing_cycle":     billingCycle,
		"end_date":          newEnd,
		"status":            database.SaasSubActive,
		"billed_months":     months,
		"discount_type":     d.Type,
		"discount_value":    d.Value,
		"notes":             notes,
		"provisional_until": nil,
		"grace_ends_at":     nil,
	}).Error; err != nil {
		return nil, err
	}
	// Cualquier otra suscripción del tenant queda como histórico: una sola vigente.
	_ = tx.Model(&database.SaasSubscription{}).
		Where("tenant_id = ? AND id <> ? AND status NOT IN ?", sub.TenantID, sub.ID,
			[]string{database.SaasSubCancelled}).
		Update("status", database.SaasSubExpired)

	syncTenantModulesFromPlanTx(tx, sub.TenantID, plan.ID)
	_ = tx.Model(&database.Tenant{}).Where("id = ?", sub.TenantID).
		Updates(map[string]interface{}{"plan": plan.Name, "status": database.TenantStatusActive}).Error

	if err := createCycleForPeriodTx(tx, sub, plan, periodStart, newEnd, months, d); err != nil {
		return nil, fmt.Errorf("ciclo de facturación: %w", err)
	}
	// Aquí el tramo nuevo no solapa con los cobros de esta misma suscripción, pero sí puede
	// hacerlo con huérfanos de suscripciones anteriores del tenant.
	if err := voidOverlappingUnpaidCyclesTx(tx, sub.TenantID, sub.ID, periodStart, newEnd); err != nil {
		return nil, fmt.Errorf("anulando cobros solapados: %w", err)
	}
	_ = tx.First(sub, sub.ID).Error
	return sub, nil
}

// createCycleForPeriodTx emite el cobro de un tramo concreto (prepago: vence al iniciarlo).
func createCycleForPeriodTx(
	tx *gorm.DB,
	sub *database.SaasSubscription,
	plan *database.SaasPlan,
	periodStart, periodEnd time.Time,
	months int,
	d Discount,
) error {
	var existing database.SaasBillingCycle
	if err := tx.Where("subscription_id = ? AND period_end = ?", sub.ID, periodEnd).
		First(&existing).Error; err == nil {
		return nil
	}
	cfg, _ := LoadSettings()
	amounts := ComputeCycleAmounts(plan.Price, months, d)
	cycle := &database.SaasBillingCycle{
		TenantID: sub.TenantID, SubscriptionID: sub.ID, PlanID: plan.ID,
		PeriodStart: periodStart, PeriodEnd: periodEnd, DueDate: periodStart,
		Amount: amounts.Net, GrossAmount: amounts.Gross, MonthsCovered: amounts.Months,
		DiscountType: amounts.Discount.Type, DiscountValue: amounts.Discount.Value,
		ReconnectionFee: cfg.ReconnectionFee, Currency: "PEN",
		Status: database.SaasInvoicePending,
	}
	if err := tx.Create(cycle).Error; err != nil {
		if isDuplicateBillingCycleErr(err) {
			return nil
		}
		return err
	}
	limit := 0
	if !plan.IsUnlimitedDocuments {
		limit = plan.MonthlyDocumentsLimit
	}
	return tx.Model(cycle).Updates(map[string]interface{}{
		"is_unlimited_documents": plan.IsUnlimitedDocuments,
		"documents_limit":        limit,
	}).Error
}

// guardBillingCycleApprove evita doble aprobación por ciclo (FOR UPDATE + 1 approved por billing_cycle).
func guardBillingCycleApprove(tx *gorm.DB, cycleID uint, paymentID uint) error {
	var cycle database.SaasBillingCycle
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&cycle, cycleID).Error; err != nil {
		return errors.New("ciclo de facturación no encontrado")
	}
	if cycle.Status == database.SaasInvoicePaid {
		return errors.New("el ciclo de facturación ya fue pagado")
	}
	var approvedCount int64
	if err := tx.Model(&database.SaasPayment{}).
		Where("billing_cycle_id = ? AND status = ? AND id <> ?", cycleID, database.SaasPayApproved, paymentID).
		Count(&approvedCount).Error; err != nil {
		return err
	}
	if approvedCount > 0 {
		return errors.New("ya existe un pago aprobado para este ciclo de facturación")
	}
	return nil
}

// supersedePriorPendingPayments cierra (rechaza) automáticamente cualquier pago
// pending_review/pending que haya quedado del mismo ciclo cuando llega un comprobante
// nuevo para ese mismo ciclo (p. ej. el anterior nunca se revisó). Complementa a
// supersedeSiblingPendingPayments: éste actúa al SUBIR un comprobante nuevo, el otro
// al APROBAR uno — juntos evitan que un comprobante viejo quede huérfano en
// pending_review para siempre.
func supersedePriorPendingPayments(tx *gorm.DB, cycleID uint, now time.Time) error {
	var prior []database.SaasPayment
	if err := tx.Where("billing_cycle_id = ? AND status IN ?",
		cycleID, []string{database.SaasPayPendingReview, database.SaasPayPending}).
		Find(&prior).Error; err != nil {
		return err
	}
	for _, p := range prior {
		note := "Superado automáticamente: se subió un comprobante más reciente para este ciclo"
		if err := tx.Model(&database.SaasPayment{}).Where("id = ?", p.ID).Updates(map[string]interface{}{
			"status":      database.SaasPayRejected,
			"admin_notes": note,
			"reviewed_at": now,
		}).Error; err != nil {
			return err
		}
		LogEventTx(tx, p.TenantID, p.SubscriptionID, EventPaymentRejected, "system", nil, note, "")
	}
	return nil
}

// supersedeSiblingPendingPayments rechaza automáticamente cualquier otro pago que haya
// quedado pending_review/pending para el mismo ciclo de facturación ya pagado por
// `approvedPaymentID`. Sin esto, un comprobante viejo sin resolver seguía marcando al
// tenant con "tienes un pago en revisión" para siempre, aunque el ciclo ya estuviera
// cerrado por un pago posterior.
func supersedeSiblingPendingPayments(tx *gorm.DB, cycleID uint, approvedPaymentID uint, reviewerID uint, now time.Time) error {
	var siblings []database.SaasPayment
	if err := tx.Where("billing_cycle_id = ? AND id <> ? AND status IN ?",
		cycleID, approvedPaymentID, []string{database.SaasPayPendingReview, database.SaasPayPending}).
		Find(&siblings).Error; err != nil {
		return err
	}
	for _, sib := range siblings {
		note := fmt.Sprintf("Superado automáticamente: el ciclo ya quedó pagado con el pago #%d", approvedPaymentID)
		if err := tx.Model(&database.SaasPayment{}).Where("id = ?", sib.ID).Updates(map[string]interface{}{
			"status":      database.SaasPayRejected,
			"admin_notes": note,
			"reviewed_by": reviewerID,
			"reviewed_at": now,
		}).Error; err != nil {
			return err
		}
		LogEventTx(tx, sib.TenantID, sib.SubscriptionID, EventPaymentRejected, "system", &reviewerID, note, "")
	}
	return nil
}

func markCyclePaidTx(tx *gorm.DB, cycleID uint, paymentID uint) error {
	now := NowLima()
	return tx.Model(&database.SaasBillingCycle{}).Where("id = ?", cycleID).
		Updates(map[string]interface{}{
			"status": database.SaasInvoicePaid, "paid_at": now, "payment_id": paymentID,
		}).Error
}

// ExtendSubscription crea o extiende suscripción (API pública).
// ExtendSubscription crea la nueva suscripción del tenant. El descuento es opcional y queda
// guardado en la suscripción, de modo que cualquier cobro que se genere para ella lo aplique.
func ExtendSubscription(tenantID uint, planID uint, months int, notes string, discount ...Discount) (*database.SaasSubscription, error) {
	var d Discount
	if len(discount) > 0 {
		d = discount[0]
	}
	norm, err := NormalizeDiscount(d)
	if err != nil {
		return nil, err
	}
	var sub *database.SaasSubscription
	err = database.CentralDB.Transaction(func(tx *gorm.DB) error {
		s, err := extendSubscriptionTx(tx, tenantID, planID, months, notes, norm)
		sub = s
		return err
	})
	if err != nil {
		return nil, err
	}
	InvalidateTenantCache(tenantID)
	return sub, nil
}

// syncTenantModulesFromPlanTx materializa los módulos del plan en el tenant. Preserva las
// CORTESÍAS (source='manual'): solo apaga los módulos heredados del plan (source='plan') y
// reactiva los del plan. Un módulo manual que no está en el plan queda activo.
func syncTenantModulesFromPlanTx(tx *gorm.DB, tenantID, planID uint) {
	planSet := make(map[string]bool)
	var pms []database.SaasPlanModule
	tx.Where("plan_id = ?", planID).Find(&pms)
	for _, pm := range pms {
		planSet[pm.ModuleKey] = true
	}
	// Apagar solo lo heredado del plan; las cortesías (source='manual') se conservan.
	tx.Model(&database.TenantModule{}).
		Where("tenant_id = ? AND source = ?", tenantID, "plan").
		Update("enabled", false)
	for key := range planSet {
		var tm database.TenantModule
		err := tx.Where("tenant_id = ? AND module_key = ?", tenantID, key).First(&tm).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			cfgJSON := "{}"
			tx.Create(&database.TenantModule{TenantID: tenantID, ModuleKey: key, Enabled: true, Source: "plan", ConfigJSON: &cfgJSON})
		} else if err == nil {
			// Ahora forma parte del plan: se activa y pasa a origen 'plan' (aunque antes fuese cortesía).
			tx.Model(&tm).Updates(map[string]interface{}{"enabled": true, "source": "plan"})
		}
	}
}

// ReconcileTenantModulesFromPlan re-sincroniza los módulos de TODOS los tenants cuya suscripción
// VIGENTE apunta a este plan. Se llama al editar los módulos de un plan (respeta cortesías).
func ReconcileTenantModulesFromPlan(planID uint) error {
	if database.CentralDB == nil {
		return nil
	}
	var tenantIDs []uint
	database.CentralDB.Model(&database.SaasSubscription{}).
		Where("plan_id = ? AND status NOT IN ?", planID, []string{database.SaasSubCancelled}).
		Distinct("tenant_id").Pluck("tenant_id", &tenantIDs)
	for _, tid := range tenantIDs {
		// Solo si su suscripción vigente (la más reciente no cancelada) es este plan.
		var cur database.SaasSubscription
		if err := database.CentralDB.Where("tenant_id = ?", tid).
			Where("status NOT IN ?", []string{database.SaasSubCancelled}).
			Order("created_at desc").First(&cur).Error; err != nil {
			continue
		}
		if cur.PlanID != planID {
			continue
		}
		if err := database.CentralDB.Transaction(func(tx *gorm.DB) error {
			syncTenantModulesFromPlanTx(tx, tid, planID)
			return nil
		}); err == nil {
			InvalidateTenantCache(tid)
		}
	}
	return nil
}

func ensureBillingCycleTx(tx *gorm.DB, sub *database.SaasSubscription) (*database.SaasBillingCycle, error) {
	if sub == nil {
		return nil, nil
	}
	var plan database.SaasPlan
	if err := tx.First(&plan, sub.PlanID).Error; err != nil {
		return nil, err
	}
	var existing database.SaasBillingCycle
	err := tx.Where("subscription_id = ? AND period_end = ?", sub.ID, sub.EndDate).First(&existing).Error
	if err == nil {
		_ = docusage.SyncCycleDocumentQuotaFromPlan(&existing, sub.PlanID)
		_ = tx.First(&existing, existing.ID).Error
		return &existing, nil
	}
	cfg, _ := LoadSettings()
	amounts := ComputeCycleAmounts(plan.Price,
		pricing.BillableMonths(sub.BilledMonths, sub.StartDate, sub.EndDate, lima()),
		Discount{Type: sub.DiscountType, Value: sub.DiscountValue})
	cycle := &database.SaasBillingCycle{
		TenantID: sub.TenantID, SubscriptionID: sub.ID, PlanID: sub.PlanID,
		// Prepago: la deuda vence al INICIO del período (se paga por adelantado), igual que la
		// vía manual (IssueRenewalInvoice). Antes vencía al final (postpago), incoherente.
		PeriodStart: sub.StartDate, PeriodEnd: sub.EndDate, DueDate: sub.StartDate,
		Amount: amounts.Net, GrossAmount: amounts.Gross, MonthsCovered: amounts.Months,
		DiscountType: amounts.Discount.Type, DiscountValue: amounts.Discount.Value,
		ReconnectionFee: cfg.ReconnectionFee, Currency: "PEN",
		Status: database.SaasInvoicePending,
	}
	if err := tx.Create(cycle).Error; err != nil {
		if isDuplicateBillingCycleErr(err) {
			if err := tx.Where("subscription_id = ? AND period_end = ?", sub.ID, sub.EndDate).
				First(&existing).Error; err != nil {
				return nil, err
			}
			return &existing, nil
		}
		return nil, err
	}
	limit := 0
	if !plan.IsUnlimitedDocuments {
		limit = plan.MonthlyDocumentsLimit
	}
	_ = tx.Model(cycle).Updates(map[string]interface{}{
		"is_unlimited_documents": plan.IsUnlimitedDocuments,
		"documents_limit":        limit,
		"documents_used":         0,
	}).Error
	return cycle, nil
}

func isDuplicateBillingCycleErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate") || strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "1062") || strings.Contains(msg, "unique")
}
