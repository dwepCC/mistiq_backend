package saas

import (
	"encoding/json"
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
			// Estado del ciclo como fuente de verdad (ver saas_models.go: SaasInvoicePendingReview):
			// no se puede subir un comprobante para un ciclo ya pagado, ni para uno anulado
			// administrativamente. Un ciclo ya en pending_review SÍ se acepta (reenvío): el
			// comprobante anterior queda superado más abajo (supersedePriorPendingPayments).
			switch c.Status {
			case database.SaasInvoicePaid:
				return errors.New("este período ya fue pagado")
			case database.SaasInvoiceRejected:
				return errors.New("esta factura fue anulada; contacta a soporte")
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
		} else {
			// Mismo problema, camino sin ciclo: una solicitud de renovación (SubmitRenewalRequest
			// — "elegir plan"/renovar sin factura previa) no tiene billing_cycle_id con el que
			// agrupar, así que supersedePriorPendingPayments no la alcanza. Sin esto, cada
			// solicitud nueva se apilaba sin cerrar la anterior y has_pending_payment_review
			// (state.go, cuenta TODO saas_payments.status=pending_review del tenant) se quedaba
			// pegado en true para siempre, aunque el tenant ya hubiera vuelto a enviar otra.
			if err := supersedePriorPendingRenewalRequestsTx(tx, in.TenantID, NowLima()); err != nil {
				return err
			}
		}

		status := database.SaasPayPendingReview
		if in.FromAdmin {
			status = database.SaasPayPending
		}

		// Snapshot de método/QR/cuentas vigentes en este instante — ver payment_method_snapshot.go.
		// Mismo punto de escritura para SubmitPayment (deuda pendiente) y SubmitRenewalRequest
		// (renovación), que delega acá — ambos flujos quedan con la misma trazabilidad.
		methodLabel, methodKind, methodDetails := paymentMethodSnapshot(cfg, in.PaymentMethod)

		p := &database.SaasPayment{
			TenantID: in.TenantID, Amount: in.Amount, Currency: "PEN",
			PeriodMonths: in.PeriodMonths, PaymentMethod: in.PaymentMethod,
			PaymentMethodLabel: methodLabel, PaymentMethodKind: methodKind, PaymentDetailsJSON: methodDetails,
			PaymentDate: in.PaymentDate, Reference: in.Reference,
			ReceiptURL: in.ReceiptURL, Notes: in.Notes, Status: status, SubmittedBy: in.SubmittedBy,
		}
		if in.PlanID > 0 {
			planID := in.PlanID
			p.RequestedPlanID = &planID
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

		// El ciclo pasa a pending_review en cuanto hay un comprobante esperando aprobación: es lo
		// que hace que /subscription/summary deje de mostrar "Pagar ahora" mientras se revisa (ver
		// RejectPayment para el camino de vuelta) y que el cron de vencidos (que solo barre
		// 'pending') deje de tocarlo. Idempotente si ya estaba en pending_review (reenvío).
		if cycle != nil && cycle.Status != database.SaasInvoicePendingReview {
			if err := tx.Model(&database.SaasBillingCycle{}).Where("id = ?", cycle.ID).
				Update("status", database.SaasInvoicePendingReview).Error; err != nil {
				return err
			}
		}

		// Cupo para otorgar provisional: por ciclo (provisional_used, 1 vez por ciclo) o, sin
		// ciclo (solicitud de plan nueva, ver renewal_request.go: SubmitRenewalRequest), por que
		// la suscripción no tenga ya un provisional vigente (evita extenderlo indefinido a punta
		// de solicitudes repetidas sin que nunca se apruebe nada).
		provisionalSlotFree := false
		if cycle != nil {
			provisionalSlotFree = !cycle.ProvisionalUsed
		} else if hasSub {
			provisionalSlotFree = sub.ProvisionalUntil == nil || sub.ProvisionalUntil.Before(NowLima())
		}
		if !in.FromAdmin && cfg.ProvisionalReactivationEnabled && hasSub && provisionalSlotFree && in.ReceiptURL != "" {
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
				if cycle != nil {
					if err := tx.Model(cycle).Update("provisional_used", true).Error; err != nil {
						return err
					}
				}
				if err := tx.Model(&tenant).Update("status", database.TenantStatusActive).Error; err != nil {
					return err
				}
				if err := tx.Model(p).Update("provisional_applied", true).Error; err != nil {
					return err
				}
				sid := sub.ID
				meta := map[string]interface{}{"payment_id": p.ID, "until": until.Format(time.RFC3339)}
				if cycle != nil {
					meta["billing_cycle_id"] = cycle.ID
				}
				LogEventTx(tx, in.TenantID, &sid, EventProvisionalGranted, "tenant", in.SubmittedBy,
					"reactivación provisional", MetaJSON(meta))
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
	var tenantID uint
	err := database.CentralDB.Transaction(func(tx *gorm.DB) error {
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

		// Foto de "cómo estaba todo antes de aprobar" — RevertApprovedPayment la usa para
		// deshacer esta aprobación con precisión (ver approvalSnapshot). Se arma acá, antes de
		// tocar nada, y se guarda en el pago recién al final (ya con el resultado de la
		// extensión, para saber si creó ciclo/suscripción nuevos).
		snap := approvalSnapshot{
			TenantPlan:           tenant.Plan,
			TenantStatus:         tenant.Status,
			TenantStrikeCount:    tenant.StrikeCount,
			TenantPaymentBlocked: tenant.PaymentBlocked,
			HadSubscription:      hasCurSub,
		}
		if hasCurSub {
			snap.PrevSubscriptionID = curSub.ID
			snap.PrevSubPlanID = curSub.PlanID
			snap.PrevSubBillingCycle = curSub.BillingCycle
			snap.PrevSubEndDate = &curSub.EndDate
			snap.PrevSubStatus = curSub.Status
			snap.PrevSubBilledMonths = curSub.BilledMonths
			snap.PrevSubDiscountType = curSub.DiscountType
			snap.PrevSubDiscountValue = curSub.DiscountValue
			snap.PrevSubProvisionalUntil = curSub.ProvisionalUntil
			snap.PrevSubGraceEndsAt = curSub.GraceEndsAt
			snap.PrevSubNotes = curSub.Notes
		}
		cycleExistedBefore := cycle != nil
		if cycleExistedBefore {
			snap.CyclePrevStatus = cycle.Status
			snap.CyclePrevPaidAt = cycle.PaidAt
			snap.CyclePrevPaymentID = cycle.PaymentID
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
			} else if payment.RequestedPlanID != nil && *payment.RequestedPlanID > 0 {
				// Sin ciclo: es una solicitud de plan del tenant (ver SubmitRenewalRequest), no un
				// cobro ya emitido. Su elección pesa más que quedarse callado con el plan viejo.
				planID = *payment.RequestedPlanID
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
			// Edge: pago sin ningún ciclo → extensión clásica por meses del plan. Es el caso de
			// una solicitud de plan de autoservicio (ver SubmitRenewalRequest): el tenant ya vio
			// y pagó el descuento del ciclo fijo que eligió — sin recuperarlo acá, la suscripción/
			// ciclo que resulta de aprobar quedaría al precio pleno, aunque haya pagado con
			// descuento.
			//
			// extendSubscriptionTx crea/toca el ciclo del tramo recién cubierto (renewInPlaceTx o
			// ensureBillingCycleTx) — ese es precisamente el ciclo que este pago paga, así que se
			// recoge en newCycle para enlazarlo abajo. Antes se perdía (quedaba "pending" para
			// siempre, sin payment_id): el pago figuraba "approved" en el panel central pero el
			// tenant seguía viendo "pendiente" con botón pagar, porque /subscription/summary lee
			// el estado desde saas_billing_cycles, no desde saas_payments.
			extendMonths := periodMonths
			if extendMonths <= 0 {
				extendMonths = payment.PeriodMonths
			}
			if extendMonths <= 0 && hasCurSub && curSub.BillingCycle != "" {
				extendMonths = CycleMonthsFromBilling(curSub.BillingCycle)
			}
			var newCycle *database.SaasBillingCycle
			sub, newCycle, err = extendSubscriptionTx(tx, payment.TenantID, planID, extendMonths,
				fmt.Sprintf("Pago #%d aprobado", paymentID), nil, PlanCycleDiscount(planID, extendMonths))
			cycle = newCycle
		}
		if err != nil {
			return err
		}
		// El pago que llegó sin billing_cycle_id (solicitud de renovación, ver
		// SubmitRenewalRequest) queda enlazado al ciclo que terminó pagando — sin esto el pago
		// aprobado se quedaba con billing_cycle_id NULL para siempre aunque el ciclo sí supiera
		// qué pago lo saldó (cycle.payment_id): el enlace solo iba en un sentido. Rompía, entre
		// otras cosas, el comprobante del tenant (InvoiceView.ReceiptURL) para ese período.
		if cycle != nil && payment.BillingCycleID == nil {
			if err := tx.Model(&payment).Update("billing_cycle_id", cycle.ID).Error; err != nil {
				return err
			}
			payment.BillingCycleID = &cycle.ID
		}

		// Completa el snapshot con el resultado de la extensión: si abrió una suscripción nueva
		// (cambio de plan) en vez de extender en sitio, y si el ciclo final no existía antes de
		// este approve (lo creó recién la extensión) — ambos datos los necesita
		// RevertApprovedPayment para saber qué deshacer y qué NO tocar.
		snap.CreatedNewSubscription = hasCurSub && sub.ID != curSub.ID
		if cycle != nil {
			snap.CycleID = cycle.ID
			snap.CycleWasCreated = !cycleExistedBefore
		}
		snapJSON, err := json.Marshal(snap)
		if err != nil {
			return fmt.Errorf("armando snapshot de reversión: %w", err)
		}

		if err := tx.Model(&payment).Updates(map[string]interface{}{
			"subscription_id":            sub.ID,
			"pre_approval_snapshot_json": string(snapJSON),
		}).Error; err != nil {
			return err
		}
		// Único punto que cierra "pago aprobado → ciclo pagado", para las dos rutas de arriba
		// (ciclo ya existente, o ciclo recién creado al extender). Antes esto solo corría para el
		// primer caso, así que un pago sin billing_cycle_id (o cuyo ciclo pendiente ya lo había
		// cerrado otro pago segundos antes) aprobaba y extendía la suscripción correctamente, pero
		// dejaba su propio ciclo huérfano en "pending".
		if cycle != nil {
			if err := markCyclePaidTx(tx, cycle.ID, payment.ID); err != nil {
				return err
			}
			// Si el mismo ciclo tenía otro(s) pago(s) que quedaron pending_review sin
			// resolver (p. ej. un comprobante viejo que nadie aprobó/rechazó y luego el
			// tenant volvió a pagar), quedaban colgados para siempre: guardBillingCycleApprove
			// solo evita aprobar dos pagos por ciclo, pero no cierra los que ya no aplican.
			// Eso hacía que /subscription siguiera mostrando "pago en revisión" indefinidamente
			// aunque el ciclo ya estuviera pagado. Se superan automáticamente al aprobar éste.
			if err := supersedeSiblingPendingPayments(tx, cycle.ID, payment.ID, reviewerID, now); err != nil {
				return err
			}
			// Cinturón de seguridad: si por algún camino futuro esto dejara de cumplirse, que
			// falle fuerte y aborte la transacción en vez de aprobar en silencio con el ciclo
			// suelto — el bug que motivó este bloque era exactamente eso, y no daba ni un error.
			var check database.SaasBillingCycle
			if err := tx.First(&check, cycle.ID).Error; err != nil {
				return err
			}
			if check.Status != database.SaasInvoicePaid || check.PaymentID == nil || *check.PaymentID != payment.ID {
				return fmt.Errorf("inconsistencia al aprobar pago #%d: el ciclo #%d no quedó pagado/enlazado", payment.ID, cycle.ID)
			}
		}

		sid := sub.ID
		if err := ClearStrikesOnApprove(tx, payment.TenantID, &sid, &reviewerID); err != nil {
			return err
		}
		LogEventTx(tx, payment.TenantID, &sid, EventPaymentApproved, "admin", &reviewerID, adminNotes, "")
		LogEventTx(tx, payment.TenantID, &sid, EventReactivated, "admin", &reviewerID, adminNotes, "")
		tenantID = payment.TenantID
		return nil
	})
	if err != nil {
		return err
	}
	// Mismo patrón que SubmitPayment/RejectPayment: invalidar tras comprometer la transacción,
	// para que /subscription/summary no siga sirviendo el estado viejo desde caché (hasta 10s).
	// Antes ApprovePayment no invalidaba nada — única mutación del paquete que se saltaba esto.
	InvalidateTenantCache(tenantID)
	return nil
}

// approvalSnapshot es la foto de "cómo estaba todo antes de aprobar este pago" — la captura
// ApprovePayment antes de tocar nada y la usa RevertApprovedPayment para deshacer la aprobación
// con precisión, en vez de reconstruir el estado previo adivinando a partir de otros ciclos.
type approvalSnapshot struct {
	TenantPlan           string `json:"tenant_plan"`
	TenantStatus         string `json:"tenant_status"`
	TenantStrikeCount    int    `json:"tenant_strike_count"`
	TenantPaymentBlocked bool   `json:"tenant_payment_blocked"`

	HadSubscription         bool       `json:"had_subscription"`
	PrevSubscriptionID      uint       `json:"prev_subscription_id,omitempty"`
	PrevSubPlanID           uint       `json:"prev_sub_plan_id,omitempty"`
	PrevSubBillingCycle     string     `json:"prev_sub_billing_cycle,omitempty"`
	PrevSubEndDate          *time.Time `json:"prev_sub_end_date,omitempty"`
	PrevSubStatus           string     `json:"prev_sub_status,omitempty"`
	PrevSubBilledMonths     int        `json:"prev_sub_billed_months,omitempty"`
	PrevSubDiscountType     string     `json:"prev_sub_discount_type,omitempty"`
	PrevSubDiscountValue    float64    `json:"prev_sub_discount_value,omitempty"`
	PrevSubProvisionalUntil *time.Time `json:"prev_sub_provisional_until,omitempty"`
	PrevSubGraceEndsAt      *time.Time `json:"prev_sub_grace_ends_at,omitempty"`
	PrevSubNotes            string     `json:"prev_sub_notes,omitempty"`

	// CreatedNewSubscription: true si aprobar este pago abrió una fila de suscripción nueva
	// (cambio de plan, ver extendSubscriptionTx) en vez de extender en sitio la vigente.
	CreatedNewSubscription bool `json:"created_new_subscription"`

	CycleID uint `json:"cycle_id,omitempty"`
	// CycleWasCreated: true si el ciclo no existía antes de aprobar (lo generó esta misma
	// aprobación) — al revertir hay que BORRARLO, no solo cambiarle el estado: el índice único
	// (subscription_id, period_end) impediría crear uno nuevo para el mismo tramo si el registro
	// viejo se queda ahí con otro estado.
	CycleWasCreated    bool       `json:"cycle_was_created"`
	CyclePrevStatus    string     `json:"cycle_prev_status,omitempty"`
	CyclePrevPaidAt    *time.Time `json:"cycle_prev_paid_at,omitempty"`
	CyclePrevPaymentID *uint      `json:"cycle_prev_payment_id,omitempty"`
}

// RevertApprovedPayment anula un pago YA APROBADO y deshace exactamente lo que esa aprobación
// produjo: la suscripción y el ciclo de facturación vuelven al estado de justo antes (o se
// eliminan, si la aprobación los había creado), para que el tenant pueda repetir el pago o la
// renovación desde cero. El pago NO se borra — queda 'reversed', con motivo y quién lo anuló,
// igual que cualquier reverso contable (auditable, no un agujero en el historial).
//
// Solo se puede revertir el ÚLTIMO pago aprobado del tenant: si hay uno posterior ya aprobado,
// ese encadenó su período desde el estado que dejó este (ver renewInPlaceTx), y deshacer este
// primero rompería esa cadena. Hay que revertir el más reciente primero.
//
// Requiere que el pago se haya aprobado DESPUÉS de este cambio (con snapshot guardado); los
// aprobados antes no tienen memoria de su estado previo y hay que ajustar la suscripción a mano.
func RevertApprovedPayment(paymentID uint, reason string, actorID uint) error {
	var tenantID uint
	err := database.CentralDB.Transaction(func(tx *gorm.DB) error {
		var payment database.SaasPayment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&payment, paymentID).Error; err != nil {
			return errors.New("pago no encontrado")
		}
		if payment.Status != database.SaasPayApproved {
			return fmt.Errorf("solo se puede anular un pago aprobado (este está %s)", payment.Status)
		}
		if payment.PreApprovalSnapshotJSON == "" {
			return errors.New("este pago se aprobó antes de existir la anulación automática; ajusta la suscripción manualmente")
		}
		tenantID = payment.TenantID

		var newer database.SaasPayment
		hasNewer := tx.Where("tenant_id = ? AND status = ? AND id <> ?", payment.TenantID, database.SaasPayApproved, payment.ID).
			Where("reviewed_at > ? OR (reviewed_at = ? AND id > ?)", payment.ReviewedAt, payment.ReviewedAt, payment.ID).
			Order("reviewed_at desc").First(&newer).Error == nil
		if hasNewer {
			return fmt.Errorf("hay un pago posterior ya aprobado (#%d) sobre esta suscripción; anula primero ese antes de revertir este", newer.ID)
		}

		var snap approvalSnapshot
		if err := json.Unmarshal([]byte(payment.PreApprovalSnapshotJSON), &snap); err != nil {
			return fmt.Errorf("snapshot de reversión corrupto: %w", err)
		}

		// Ciclo: si lo creó esta aprobación, se borra (no solo se despaga) — dejarlo con otro
		// estado bloquearía crear uno nuevo para el mismo tramo cuando el tenant vuelva a pagar,
		// por el índice único (subscription_id, period_end). Si ya existía, vuelve exactamente a
		// como estaba (normalmente: pending, sin paid_at ni payment_id).
		if snap.CycleID > 0 {
			var otherPayment database.SaasPayment
			if tx.Where("billing_cycle_id = ? AND id <> ?", snap.CycleID, payment.ID).
				First(&otherPayment).Error == nil {
				return fmt.Errorf("el ciclo #%d tiene otro pago (#%d) enlazado; resuélvelo antes de anular este", snap.CycleID, otherPayment.ID)
			}
			if snap.CycleWasCreated {
				if err := tx.Where("id = ?", snap.CycleID).Delete(&database.SaasBillingCycle{}).Error; err != nil {
					return err
				}
			} else {
				if err := tx.Model(&database.SaasBillingCycle{}).Where("id = ?", snap.CycleID).
					Updates(map[string]interface{}{
						"status": snap.CyclePrevStatus, "paid_at": snap.CyclePrevPaidAt, "payment_id": snap.CyclePrevPaymentID,
					}).Error; err != nil {
					return err
				}
			}
		}

		var subID *uint
		if snap.CreatedNewSubscription {
			// Esta aprobación abrió una suscripción nueva (cambio de plan): se borra por completo
			// y se reactiva la anterior, que había quedado 'expired'.
			//
			// Nota: si abrirla anuló (rejected) cobros solapados de la suscripción anterior vía
			// voidOverlappingUnpaidCyclesTx, esos NO se restauran automáticamente acá — revisa
			// saas_billing_cycles del tenant a mano si este caso (revertir un cambio de plan) aplica.
			if payment.SubscriptionID != nil {
				if err := tx.Where("id = ?", *payment.SubscriptionID).Delete(&database.SaasSubscription{}).Error; err != nil {
					return err
				}
			}
			if snap.HadSubscription {
				if err := tx.Model(&database.SaasSubscription{}).Where("id = ?", snap.PrevSubscriptionID).
					Update("status", snap.PrevSubStatus).Error; err != nil {
					return err
				}
				sid := snap.PrevSubscriptionID
				subID = &sid
			}
		} else if snap.HadSubscription {
			// Caso normal de una renovación: extensión en sitio → la suscripción vuelve a sus
			// valores de antes de aprobar.
			if err := tx.Model(&database.SaasSubscription{}).Where("id = ?", snap.PrevSubscriptionID).
				Updates(map[string]interface{}{
					"plan_id": snap.PrevSubPlanID, "billing_cycle": snap.PrevSubBillingCycle,
					"end_date": snap.PrevSubEndDate, "status": snap.PrevSubStatus,
					"billed_months": snap.PrevSubBilledMonths,
					"discount_type": snap.PrevSubDiscountType, "discount_value": snap.PrevSubDiscountValue,
					"provisional_until": snap.PrevSubProvisionalUntil, "grace_ends_at": snap.PrevSubGraceEndsAt,
					"notes": snap.PrevSubNotes,
				}).Error; err != nil {
				return err
			}
			sid := snap.PrevSubscriptionID
			subID = &sid
		}

		if err := tx.Model(&database.Tenant{}).Where("id = ?", payment.TenantID).
			Updates(map[string]interface{}{
				"plan": snap.TenantPlan, "status": snap.TenantStatus,
				"strike_count": snap.TenantStrikeCount, "payment_blocked": snap.TenantPaymentBlocked,
			}).Error; err != nil {
			return err
		}

		now := NowLima()
		if err := tx.Model(&payment).Updates(map[string]interface{}{
			"status": database.SaasPayReversed, "reversed_at": now, "reversed_by": actorID,
			"reversal_reason": reason,
		}).Error; err != nil {
			return err
		}
		LogEventTx(tx, payment.TenantID, subID, EventPaymentReversed, "admin", &actorID, reason, "")
		return nil
	})
	if err != nil {
		return err
	}
	InvalidateTenantCache(tenantID)
	return nil
}

// PlanCycleDiscount descuento del ciclo fijo del plan (si `months` calza con uno habilitado,
// ver FixedPlanCycleMonths) — la MISMA tabla que ya usa el autoservicio del tenant
// (SubmitRenewalRequest) y la aprobación de un pago sin ciclo (ver abajo). Exportada para que
// el alta de empresas desde el panel central (internal/superadmin/service/tenant_service.go)
// también la use: el descuento de una empresa nueva no debe depender de lo que el admin
// escriba a mano, sino de lo que el plan tiene configurado para esos meses.
// Lectura fuera de cualquier transacción a propósito (mismo patrón que LoadSettings dentro de
// SubmitPayment): es una lectura pura, SaasPlanCycle no se modifica acá.
func PlanCycleDiscount(planID uint, months int) Discount {
	var plan database.SaasPlan
	if database.CentralDB.First(&plan, planID).Error != nil {
		return Discount{}
	}
	views := BuildPlanCycleViews(plan, LoadPlanCycles(planID))
	if c := FindEnabledPlanCycle(views, months); c != nil {
		return Discount{Type: c.DiscountType, Value: c.DiscountValue}
	}
	return Discount{}
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
	var tenantBlocked bool
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

		// El ciclo vuelve de pending_review a un estado real (pending si su plazo sigue vigente,
		// overdue si ya se agotó) — es lo que hace reaparecer el botón "Pagar ahora" en el panel
		// del tenant SOLO al rechazar, y no antes. Mismo criterio de plazo que usa el cron diario
		// (RunLimaDailyEvaluation), para no reabrir un ciclo como "pending" un día que el cron ya
		// lo habría marcado "overdue".
		if payment.BillingCycleID != nil {
			if err := revertCycleAfterRejectionTx(tx, *payment.BillingCycleID, now); err != nil {
				return err
			}
		}

		var subID *uint
		if payment.SubscriptionID != nil {
			subID = payment.SubscriptionID
		}
		// Qué le pasa a la suscripción (revertir provisional, suspender o no) lo decide
		// ApplyStrikeOnReject en un solo lugar, respetando la gracia por calendario — ver su
		// doc. Antes había un segundo `Updates` acá mismo que siempre forzaba `suspended`,
		// pisando esa decisión.
		_, blocked, err := ApplyStrikeOnReject(tx, payment.TenantID, subID, &reviewerID, adminNotes)
		if err != nil {
			return err
		}
		tenantBlocked = blocked
		return nil
	})
	if err != nil {
		return err
	}
	InvalidateTenantCache(tenantID)
	// Fuera de la transacción a propósito (mismo patrón que SubmitPayment): QueueNotification
	// escribe con database.CentralDB, no con `tx` — llamarla dentro de la transacción abierta
	// competía por el mismo write-lock (en SQLite: SQLITE_BUSY; en cualquier motor, además,
	// una notificación que "ya se envió" antes de que el commit sea definitivo).
	if tenantBlocked {
		QueueNotification(tenantID, 0, "in_app", "tenant_blocked", map[string]interface{}{"reason": adminNotes})
	}
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

// revertCycleAfterRejectionTx saca un ciclo de pending_review al rechazar el pago que lo había
// puesto ahí, devolviéndolo a 'pending' o 'overdue' según si su plazo de pago (mismo cálculo que
// el cron RunLimaDailyEvaluation) ya se agotó o no. No hace nada si el ciclo no existe o ya no
// está en pending_review (p. ej. lo superó un pago posterior — ver supersedePriorPendingPayments).
func revertCycleAfterRejectionTx(tx *gorm.DB, cycleID uint, now time.Time) error {
	var cycle database.SaasBillingCycle
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&cycle, cycleID).Error; err != nil {
		return nil // ciclo no encontrado: nada que revertir
	}
	if cycle.Status != database.SaasInvoicePendingReview {
		return nil
	}
	cfg, _ := LoadSettings()
	payWindow := EffectivePaymentWindowDays(cfg)
	overdueLimit := CalendarDateLima(now).AddDate(0, 0, -payWindow)
	newStatus := database.SaasInvoicePending
	if cycle.DueDate.Before(overdueLimit) {
		newStatus = database.SaasInvoiceOverdue
	}
	return tx.Model(&database.SaasBillingCycle{}).Where("id = ?", cycleID).
		Update("status", newStatus).Error
}

// startDate: piso opcional para el inicio de una suscripción SIN nada previo que continuar (alta
// nueva, o tenant sin suscripción vigente). nil = arranca hoy, como antes. Si se pasa, debe ser
// hoy o una fecha futura (se valida acá, no confiar solo en el frontend); típico caso de uso: se
// registra la empresa hoy pero su suscripción/cobro real arranca unos días después. Se ignora
// por completo en la rama de renovación en sitio (mismo plan): esa SIEMPRE encadena
// automáticamente desde el fin de la suscripción vigente, nunca se elige a mano.
// extendSubscriptionTx extiende/crea la suscripción y devuelve, junto con ella, el ciclo de
// facturación que cubre el tramo recién añadido — quien llama (p. ej. ApprovePayment) lo necesita
// para poder enlazarlo al pago que lo originó; ver comentario en ApprovePayment.
func extendSubscriptionTx(tx *gorm.DB, tenantID uint, planID uint, months int, notes string, startDate *time.Time, discount ...Discount) (*database.SaasSubscription, *database.SaasBillingCycle, error) {
	var d Discount
	if len(discount) > 0 {
		d = discount[0]
	}
	if tenantID == 0 || planID == 0 {
		return nil, nil, errors.New("tenant_id y plan_id requeridos")
	}
	var plan database.SaasPlan
	if err := tx.First(&plan, planID).Error; err != nil {
		return nil, nil, errors.New("plan no encontrado")
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
	base := CalendarDateLima(now)
	if startDate != nil {
		requested := CalendarDateLima(*startDate)
		if requested.Before(base) {
			return nil, nil, errors.New("la fecha de inicio no puede ser anterior a hoy")
		}
		base = requested
	}
	var prev database.SaasSubscription
	if err := tx.Where("tenant_id = ?", tenantID).Order("end_date desc").First(&prev).Error; err == nil {
		prevDay := CalendarDateLima(prev.EndDate)
		if prevDay.After(base) {
			base = prevDay
		}
	}
	endDay := base.AddDate(0, months, 0)

	// Con startDate explícito, el inicio real es esa fecha (inicio del día en Lima), no el
	// instante exacto de este request — así "arranca el 15" no queda con la hora de cuando el
	// admin cargó el alta.
	subStart := now
	if startDate != nil {
		subStart = base
	}

	sub := &database.SaasSubscription{
		TenantID: tenantID, PlanID: planID, BillingCycle: cycle,
		StartDate: subStart, EndDate: EndOfDayLima(endDay),
		Status: database.SaasSubActive, Notes: notes,
		BilledMonths: months, DiscountType: d.Type, DiscountValue: d.Value,
	}
	if err := tx.Create(sub).Error; err != nil {
		return nil, nil, err
	}
	syncTenantModulesFromPlanTx(tx, tenantID, planID)
	_ = tx.Model(&database.Tenant{}).Where("id = ?", tenantID).
		Updates(map[string]interface{}{"plan": plan.Name, "status": database.TenantStatusActive}).Error
	newCycle, err := ensureBillingCycleTx(tx, sub)
	if err != nil {
		return nil, nil, fmt.Errorf("ciclo de facturación: %w", err)
	}
	if err := voidOverlappingUnpaidCyclesTx(tx, tenantID, sub.ID, sub.StartDate, sub.EndDate); err != nil {
		return nil, nil, fmt.Errorf("anulando cobros solapados: %w", err)
	}
	return sub, newCycle, nil
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
		[]string{database.SaasInvoicePending, database.SaasInvoiceOverdue, database.SaasInvoicePendingReview}).
		Find(&cycles).Error; err != nil {
		return err
	}
	for i := range cycles {
		c := &cycles[i]
		// Intervalos [inicio, fin) que se cruzan en al menos un día.
		if !c.PeriodStart.Before(newEnd) || !newStart.Before(c.PeriodEnd) {
			continue
		}
		// Si había un comprobante en revisión sobre este ciclo, se rechaza en cascada: el cobro
		// que cubría ya no existe (lo reemplazó la nueva suscripción), así que dejar el pago
		// "en revisión" para siempre sería mostrarle al tenant una espera que nunca se resuelve.
		if c.Status == database.SaasInvoicePendingReview {
			if err := CascadeRejectPendingPaymentsForCycleTx(tx, c.ID, "system", nil,
				"Rechazado automáticamente: el período quedó cubierto por una nueva suscripción"); err != nil {
				return err
			}
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

// CascadeRejectPendingPaymentsForCycleTx rechaza automáticamente cualquier pago pending/
// pending_review que haya quedado ligado a un ciclo que se está anulando por una causa
// administrativa o del sistema (el cobro que cubrían deja de existir) — no por una decisión
// sobre el comprobante en sí. Evita que el pago quede huérfano "en revisión" para siempre.
// Exportada: la usa tanto pkg/saas (voidOverlappingUnpaidCyclesTx, CancelInvoice) como
// internal/subscriptions/service (Cancel de suscripción completa).
func CascadeRejectPendingPaymentsForCycleTx(tx *gorm.DB, cycleID uint, actorType string, actorID *uint, reason string) error {
	var pending []database.SaasPayment
	if err := tx.Where("billing_cycle_id = ? AND status IN ?", cycleID,
		[]string{database.SaasPayPending, database.SaasPayPendingReview}).Find(&pending).Error; err != nil {
		return err
	}
	now := NowLima()
	for _, p := range pending {
		if err := tx.Model(&database.SaasPayment{}).Where("id = ?", p.ID).Updates(map[string]interface{}{
			"status": database.SaasPayRejected, "admin_notes": reason,
			"reviewed_by": actorID, "reviewed_at": now,
		}).Error; err != nil {
			return err
		}
		LogEventTx(tx, p.TenantID, p.SubscriptionID, EventPaymentRejected, actorType, actorID, reason, "")
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
) (*database.SaasSubscription, *database.SaasBillingCycle, error) {
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
		return nil, nil, err
	}
	// Cualquier otra suscripción del tenant queda como histórico: una sola vigente.
	_ = tx.Model(&database.SaasSubscription{}).
		Where("tenant_id = ? AND id <> ? AND status NOT IN ?", sub.TenantID, sub.ID,
			[]string{database.SaasSubCancelled}).
		Update("status", database.SaasSubExpired)

	syncTenantModulesFromPlanTx(tx, sub.TenantID, plan.ID)
	_ = tx.Model(&database.Tenant{}).Where("id = ?", sub.TenantID).
		Updates(map[string]interface{}{"plan": plan.Name, "status": database.TenantStatusActive}).Error

	newCycle, err := createCycleForPeriodTx(tx, sub, plan, periodStart, newEnd, months, d)
	if err != nil {
		return nil, nil, fmt.Errorf("ciclo de facturación: %w", err)
	}
	// Aquí el tramo nuevo no solapa con los cobros de esta misma suscripción, pero sí puede
	// hacerlo con huérfanos de suscripciones anteriores del tenant.
	if err := voidOverlappingUnpaidCyclesTx(tx, sub.TenantID, sub.ID, periodStart, newEnd); err != nil {
		return nil, nil, fmt.Errorf("anulando cobros solapados: %w", err)
	}
	_ = tx.First(sub, sub.ID).Error
	return sub, newCycle, nil
}

// createCycleForPeriodTx emite el cobro de un tramo concreto (prepago: vence al iniciarlo).
// Devuelve siempre el ciclo vigente para ese tramo (el recién creado, o el que ya existía),
// para que quien llama pueda enlazarlo a un pago si corresponde — nunca lo deja "perdido".
func createCycleForPeriodTx(
	tx *gorm.DB,
	sub *database.SaasSubscription,
	plan *database.SaasPlan,
	periodStart, periodEnd time.Time,
	months int,
	d Discount,
) (*database.SaasBillingCycle, error) {
	var existing database.SaasBillingCycle
	if err := tx.Where("subscription_id = ? AND period_end = ?", sub.ID, periodEnd).
		First(&existing).Error; err == nil {
		return &existing, nil
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
			var raced database.SaasBillingCycle
			if err2 := tx.Where("subscription_id = ? AND period_end = ?", sub.ID, periodEnd).
				First(&raced).Error; err2 != nil {
				return nil, err2
			}
			return &raced, nil
		}
		return nil, err
	}
	limit := 0
	if !plan.IsUnlimitedDocuments {
		limit = plan.MonthlyDocumentsLimit
	}
	if err := tx.Model(cycle).Updates(map[string]interface{}{
		"is_unlimited_documents": plan.IsUnlimitedDocuments,
		"documents_limit":        limit,
	}).Error; err != nil {
		return nil, err
	}
	return cycle, nil
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

// supersedePriorPendingRenewalRequestsTx es supersedePriorPendingPayments para el camino SIN
// ciclo (SubmitRenewalRequest — "elegir plan"/renovar sin factura previa emitida): agrupa por
// tenant_id en vez de billing_cycle_id, ya que estos pagos nunca tienen uno.
func supersedePriorPendingRenewalRequestsTx(tx *gorm.DB, tenantID uint, now time.Time) error {
	var prior []database.SaasPayment
	if err := tx.Where("tenant_id = ? AND billing_cycle_id IS NULL AND status IN ?",
		tenantID, []string{database.SaasPayPendingReview, database.SaasPayPending}).
		Find(&prior).Error; err != nil {
		return err
	}
	for _, p := range prior {
		note := "Superado automáticamente: se envió una solicitud de renovación más reciente"
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
// startDate: ver extendSubscriptionTx — nil = arranca hoy (comportamiento de siempre).
func ExtendSubscription(tenantID uint, planID uint, months int, notes string, startDate *time.Time, discount ...Discount) (*database.SaasSubscription, error) {
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
		// Alta/extensión manual sin pago detrás: el ciclo del tramo nuevo queda "pending" a
		// propósito (es deuda real por cobrar), por eso se descarta acá.
		s, _, err := extendSubscriptionTx(tx, tenantID, planID, months, notes, startDate, norm)
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
