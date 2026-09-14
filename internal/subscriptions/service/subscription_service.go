package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/pagination"
	"tukifac/pkg/saas"

	"gorm.io/gorm"
)

type SubscriptionService struct{}

func NewSubscriptionService() *SubscriptionService { return &SubscriptionService{} }

type SubscriptionDetail struct {
	database.SaasSubscription
	PlanName       string `json:"plan_name"`
	TenantName     string `json:"tenant_name"`
	Modules        []string `json:"modules"`
	Status         string `json:"status"` // estado efectivo (active, grace_period, overdue, suspended)
	StatusLabel    string `json:"status_label"` // traducción al español
	DaysOverdue    int    `json:"days_overdue"` // días de mora (0 si vigente)
	DaysInGrace    int    `json:"days_in_grace"` // días restantes de gracia (0 si fuera de gracia)
	// IsCurrent: es la suscripción que gobierna hoy al tenant (la última no cancelada). Un
	// tenant solo tiene una vigente; las anteriores quedan como histórico al renovar.
	IsCurrent bool `json:"is_current"`
}

type SubscriptionListParams struct {
	Status string
	// BilledMonths: filtro por ciclo (1 mensual, 3 trimestral, 6 semestral, 12 anual...).
	// Se filtra por billed_months, NO por billing_cycle: ese último solo copia el
	// billing_cycle ESTÁTICO del plan (casi siempre "monthly" en el catálogo actual) y no
	// cambia según cuántos meses se contrataron en esta suscripción/renovación puntual —
	// billed_months sí, es el campo que el propio modelo documenta como "meses VENDIDOS en
	// esta suscripción, que es lo que se cobra" (ver database.SaasSubscription.BilledMonths).
	BilledMonths int
	Query        string
	// EndDateFrom/EndDateTo: filtro por vencimiento (YYYY-MM-DD, inclusive en ambos extremos)
	// sobre saas_subscriptions.end_date. Cubre los tres casos de uso del panel: "por vencer"
	// (EndDateTo = hoy + N días), "vence en tal mes" (primer/último día del mes) y "ya
	// vencieron" (EndDateTo = ayer, o combinado con Status=expired).
	EndDateFrom string
	EndDateTo   string
	Page        int
	PerPage     int
}

func (s *SubscriptionService) List(params SubscriptionListParams) ([]SubscriptionDetail, int64, error) {
	page, perPage := pagination.Normalize(params.Page, params.PerPage)
	q := database.CentralDB.Model(&database.SaasSubscription{})
	if params.Status != "" {
		q = q.Where("saas_subscriptions.status = ?", params.Status)
	}
	if params.BilledMonths > 0 {
		q = q.Where("saas_subscriptions.billed_months = ?", params.BilledMonths)
	}
	if from, err := time.Parse("2006-01-02", params.EndDateFrom); err == nil {
		q = q.Where("saas_subscriptions.end_date >= ?", from)
	}
	if to, err := time.Parse("2006-01-02", params.EndDateTo); err == nil {
		q = q.Where("saas_subscriptions.end_date < ?", to.AddDate(0, 0, 1))
	}
	if strings.TrimSpace(params.Query) != "" {
		like := "%" + strings.TrimSpace(params.Query) + "%"
		q = q.Joins("JOIN tenants ON tenants.id = saas_subscriptions.tenant_id").
			Where("tenants.name LIKE ? OR tenants.ruc LIKE ? OR tenants.slug LIKE ?", like, like, like)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var subs []database.SaasSubscription
	if err := q.Order("saas_subscriptions.created_at desc").
		Limit(perPage).
		Offset(pagination.Offset(page, perPage)).
		Find(&subs).Error; err != nil {
		return nil, 0, err
	}

	tenantNames := map[uint]string{}
	currentSubByTenant := map[uint]uint{}
	if len(subs) > 0 {
		ids := make([]uint, 0, len(subs))
		seen := map[uint]bool{}
		for _, sub := range subs {
			if !seen[sub.TenantID] {
				seen[sub.TenantID] = true
				ids = append(ids, sub.TenantID)
			}
		}
		var tenants []database.Tenant
		database.CentralDB.Select("id", "name").Where("id IN ?", ids).Find(&tenants)
		for _, t := range tenants {
			tenantNames[t.ID] = t.Name
		}
		// Misma regla que usa el runtime del tenant (computeTenantView): manda la última no
		// cancelada. Las anteriores son histórico aunque su end_date siga en el futuro.
		for _, tid := range ids {
			var cur database.SaasSubscription
			if database.CentralDB.Where("tenant_id = ?", tid).
				Where("status NOT IN ?", []string{database.SaasSubCancelled}).
				Order("created_at desc").First(&cur).Error == nil {
				currentSubByTenant[tid] = cur.ID
			}
		}
	}

	cfg, _ := saas.LoadSettings()
	now := saas.NowLima()
	result := make([]SubscriptionDetail, 0, len(subs))
	for _, sub := range subs {
		detail := SubscriptionDetail{
			SaasSubscription: sub,
			TenantName:       tenantNames[sub.TenantID],
		}
		var plan database.SaasPlan
		database.CentralDB.First(&plan, sub.PlanID)
		detail.PlanName = plan.Name
		detail.Modules = s.getPlanModules(sub.PlanID)
		detail.IsCurrent = currentSubByTenant[sub.TenantID] == sub.ID

		// El estado efectivo solo tiene sentido para la vigente. Recalcularlo en las
		// reemplazadas las pintaba «Activa» siempre que su end_date siguiera en el futuro
		// —que es lo normal al renovar antes de tiempo—, así que un tenant con varias
		// renovaciones aparentaba tener varias suscripciones activas a la vez.
		if detail.IsCurrent {
			var tenant database.Tenant
			database.CentralDB.First(&tenant, sub.TenantID)
			detail.Status = saas.ResolveEffectiveStatus(&sub, &tenant, now, cfg.GracePeriodDays)
		} else {
			detail.Status = sub.Status
		}
		enrichSubscriptionDetail(&detail)
		result = append(result, detail)
	}
	return result, total, nil
}

func (s *SubscriptionService) GetByTenant(tenantID uint) (*SubscriptionDetail, error) {
	view, err := saas.GetTenantView(tenantID)
	if err != nil || !view.HasSubscription {
		return nil, errors.New("sin suscripción activa")
	}
	var sub database.SaasSubscription
	database.CentralDB.First(&sub, view.SubscriptionID)
	// GetTenantView ya resuelve la vigente, así que esta siempre lo es.
	detail := &SubscriptionDetail{SaasSubscription: sub, PlanName: view.PlanName, IsCurrent: true}
	detail.Modules = s.getPlanModules(sub.PlanID)
	detail.Status = view.Status
	enrichSubscriptionDetail(detail)
	return detail, nil
}

type CreateSubscriptionInput struct {
	TenantID uint   `json:"tenant_id"`
	PlanID   uint   `json:"plan_id"`
	Months   int    `json:"months"`
	Notes    string `json:"notes"`
	// StartDate opcional (YYYY-MM-DD): igual que en el alta de tenant, para cuando la
	// suscripción real arranca unos días después de crearla. Vacío = arranca hoy. Solo tiene
	// efecto si el tenant NO tiene ya una suscripción vigente con este mismo plan (ese caso
	// siempre encadena en sitio desde el fin de la vigente, sin importar StartDate).
	StartDate string `json:"start_date"`
	// Descuento opcional sobre el cobro (precio del plan × meses). Pensado para contratos
	// largos: 6 meses o anual a cambio de un porcentaje o un monto fijo menos.
	DiscountType  string  `json:"discount_type"`
	DiscountValue float64 `json:"discount_value"`
}

func (s *SubscriptionService) Create(input CreateSubscriptionInput) (*database.SaasSubscription, error) {
	var startDate *time.Time
	if sd := strings.TrimSpace(input.StartDate); sd != "" {
		t, parseErr := time.ParseInLocation("2006-01-02", sd, saas.LimaLocation())
		if parseErr != nil {
			return nil, errors.New("fecha de inicio inválida (formato esperado AAAA-MM-DD)")
		}
		startDate = &t
	}
	sub, err := saas.ExtendSubscription(input.TenantID, input.PlanID, input.Months, input.Notes, startDate,
		saas.Discount{Type: input.DiscountType, Value: input.DiscountValue})
	if err != nil {
		return nil, err
	}
	saas.LogEvent(input.TenantID, &sub.ID, "subscription_created", "admin", nil, input.Notes, "")
	return sub, nil
}

// ErrSubscriptionNotCurrent: se intentó operar sobre una suscripción ya reemplazada.
var ErrSubscriptionNotCurrent = errors.New("esta suscripción es histórica (fue reemplazada por una renovación); opera sobre la vigente del tenant")

// requireCurrentSubscription impide actuar sobre el histórico de renovaciones.
//
// Las acciones del panel afectan al TENANT, no a la fila: suspender una suscripción vieja
// dejaba al tenant suspendido pese a tener una vigente al día, y «reactivar» una vieja
// llamaba a ExtendSubscription, que crea otra suscripción y expira la vigente.
func requireCurrentSubscription(sub *database.SaasSubscription) error {
	var cur database.SaasSubscription
	if err := database.CentralDB.Where("tenant_id = ?", sub.TenantID).
		Where("status NOT IN ?", []string{database.SaasSubCancelled}).
		Order("created_at desc").First(&cur).Error; err != nil {
		return nil // sin vigente identificable: no bloquear la operación
	}
	if cur.ID != sub.ID {
		return ErrSubscriptionNotCurrent
	}
	return nil
}

func (s *SubscriptionService) Suspend(id uint, reason string) error {
	var sub database.SaasSubscription
	if err := database.CentralDB.First(&sub, id).Error; err != nil {
		return errors.New("suscripción no encontrada")
	}
	if err := requireCurrentSubscription(&sub); err != nil {
		return err
	}
	database.CentralDB.Model(&sub).Updates(map[string]interface{}{
		"status": database.SaasSubSuspended, "provisional_until": nil,
	})
	database.CentralDB.Model(&database.Tenant{}).Where("id = ?", sub.TenantID).Update("status", "suspended")
	sid := sub.ID
	saas.LogEvent(sub.TenantID, &sid, "suspended", "admin", nil, reason, "")
	saas.InvalidateTenantCache(sub.TenantID)
	return nil
}

func (s *SubscriptionService) Reactivate(id uint, extraMonths int) error {
	var sub database.SaasSubscription
	if err := database.CentralDB.First(&sub, id).Error; err != nil {
		return errors.New("suscripción no encontrada")
	}
	if err := requireCurrentSubscription(&sub); err != nil {
		return err
	}
	_, err := saas.ExtendSubscription(sub.TenantID, sub.PlanID, extraMonths, "reactivación manual", nil)
	if err != nil {
		return err
	}
	sid := sub.ID
	saas.LogEvent(sub.TenantID, &sid, "reactivated", "admin", nil, "", "")
	return nil
}

// Cancel anula la suscripción: el alta no se concretó o el cliente se da de baja.
//
// A diferencia de Suspend —que es una pausa reversible pagando—, aquí la suscripción queda
// cerrada y sus cobros pendientes se anulan para que no sigan apareciendo como deuda por
// cobrar. Los datos del tenant NO se tocan: si vuelve, se le crea una suscripción nueva.
func (s *SubscriptionService) Cancel(id uint, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("el motivo es obligatorio")
	}
	var sub database.SaasSubscription
	if err := database.CentralDB.First(&sub, id).Error; err != nil {
		return errors.New("suscripción no encontrada")
	}
	if err := requireCurrentSubscription(&sub); err != nil {
		return err
	}
	if sub.Status == database.SaasSubCancelled {
		return errors.New("la suscripción ya está anulada")
	}

	if err := database.CentralDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&sub).Updates(map[string]interface{}{
			"status": database.SaasSubCancelled, "provisional_until": nil,
		}).Error; err != nil {
			return err
		}
		var cycles []database.SaasBillingCycle
		if err := tx.Where("subscription_id = ? AND status IN ?", sub.ID,
			[]string{database.SaasInvoicePending, database.SaasInvoiceOverdue, database.SaasInvoicePendingReview}).
			Find(&cycles).Error; err != nil {
			return err
		}
		for i := range cycles {
			c := &cycles[i]
			// Igual que al anular un cobro individual: un comprobante en revisión sobre un ciclo
			// que la cancelación de la suscripción va a anular se rechaza en cascada, no se deja
			// huérfano "en revisión" para siempre.
			if c.Status == database.SaasInvoicePendingReview {
				if err := saas.CascadeRejectPendingPaymentsForCycleTx(tx, c.ID, "admin", nil,
					fmt.Sprintf("Rechazado automáticamente: la suscripción #%d fue anulada (%s)", sub.ID, reason)); err != nil {
					return err
				}
			}
			if err := tx.Model(c).Update("status", database.SaasInvoiceRejected).Error; err != nil {
				return err
			}
		}
		return tx.Model(&database.Tenant{}).Where("id = ?", sub.TenantID).
			Update("status", database.TenantStatusSuspended).Error
	}); err != nil {
		return err
	}

	sid := sub.ID
	saas.LogEvent(sub.TenantID, &sid, "cancelled", "admin", nil, reason, "")
	saas.InvalidateTenantCache(sub.TenantID)
	return nil
}

// PendingCycleForSubscription cobro impago de la suscripción (nil si no hay).
func (s *SubscriptionService) PendingCycleForSubscription(subID uint) *database.SaasBillingCycle {
	var cycle database.SaasBillingCycle
	if database.CentralDB.Where("subscription_id = ? AND status IN ?", subID,
		[]string{database.SaasInvoicePending, database.SaasInvoiceOverdue}).
		Order("due_date asc").First(&cycle).Error != nil {
		return nil
	}
	return &cycle
}

func (s *SubscriptionService) CheckExpirations() int {
	_, _, suspended := saas.RunDailyJobs()
	return suspended
}

func (s *SubscriptionService) getPlanModules(planID uint) []string {
	modules := make([]string, 0)
	var pms []database.SaasPlanModule
	database.CentralDB.Where("plan_id = ?", planID).Find(&pms)
	for _, pm := range pms {
		modules = append(modules, pm.ModuleKey)
	}
	return modules
}

// ListEvents auditoría por tenant.
func (s *SubscriptionService) ListEvents(tenantID uint) ([]database.SaasSubscriptionEvent, error) {
	var events []database.SaasSubscriptionEvent
	err := database.CentralDB.Where("tenant_id = ?", tenantID).Order("created_at desc").Limit(100).Find(&events).Error
	return events, err
}

// ListNotificationLogs por tenant.
func (s *SubscriptionService) ListNotificationLogs(tenantID uint) ([]database.SaasNotificationLog, error) {
	var logs []database.SaasNotificationLog
	err := database.CentralDB.Where("tenant_id = ?", tenantID).Order("created_at desc").Limit(100).Find(&logs).Error
	return logs, err
}

type AdjustValidityInput struct {
	EndDate string `json:"end_date"`
	Reason  string `json:"reason"`
}

// AdjustValidity corrige end_date sin alterar plan ni módulos. El ciclo de facturación
// pendiente sí se realinea al nuevo vencimiento (ver más abajo): dejarlo desfasado hacía
// que el tenant viera una fecha de próximo pago que ya no correspondía.
func (s *SubscriptionService) AdjustValidity(id, saUserID uint, clientIP string, input AdjustValidityInput) (*database.SaasSubscription, error) {
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return nil, errors.New("el motivo es obligatorio")
	}
	endDay, err := parseEndDateLima(input.EndDate)
	if err != nil {
		return nil, err
	}
	newEnd := saas.EndOfDayLima(endDay)

	var sub database.SaasSubscription
	if err := database.CentralDB.First(&sub, id).Error; err != nil {
		return nil, errors.New("suscripción no encontrada")
	}

	if err := requireCurrentSubscription(&sub); err != nil {
		return nil, err
	}

	var tenant database.Tenant
	if err := database.CentralDB.First(&tenant, sub.TenantID).Error; err != nil {
		return nil, errors.New("tenant no encontrado")
	}

	// Bloquear si hay un comprobante en revisión: mover la vigencia ahora dejaría el ciclo que
	// el admin está a punto de aprobar/rechazar desalineado con el nuevo vencimiento (o, peor,
	// realignCurrentBillingCycle lo saltearía en silencio por no calzar con pending/overdue).
	// Resolver primero el pago; ajustar vigencia después.
	var reviewCount int64
	database.CentralDB.Model(&database.SaasBillingCycle{}).
		Where("subscription_id = ? AND status = ?", sub.ID, database.SaasInvoicePendingReview).
		Count(&reviewCount)
	if reviewCount > 0 {
		return nil, errors.New("hay un comprobante en revisión para esta suscripción; apruébalo o recházalo antes de ajustar la vigencia")
	}

	manuallySuspended := sub.Status == database.SaasSubSuspended
	oldEndStr := sub.EndDate.In(saas.LimaLocation()).Format("2006-01-02")
	newEndStr := newEnd.In(saas.LimaLocation()).Format("2006-01-02")

	// Vencimiento y ciclo van juntos o no van: si el realineo falla, mover end_date igual
	// dejaba la suscripción con una vigencia que ningún cobro respaldaba (el operador veía el
	// error y creía que no se había aplicado nada, pero la fecha ya estaba cambiada).
	oldEnd := sub.EndDate
	if err := database.CentralDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&sub).Update("end_date", newEnd).Error; err != nil {
			return err
		}
		return realignCurrentBillingCycle(tx, sub.ID, oldEnd, newEnd)
	}); err != nil {
		return nil, err
	}
	sub.EndDate = newEnd

	cfg, _ := saas.LoadSettings()
	now := saas.NowLima()
	effective := saas.ResolveEffectiveStatus(&sub, &tenant, now, cfg.GracePeriodDays)

	if !manuallySuspended && sub.Status != database.SaasSubCancelled {
		newStatus := recalcStoredSubscriptionStatus(&sub, effective, now, cfg.GracePeriodDays)
		if newStatus != sub.Status {
			_ = database.CentralDB.Model(&sub).Update("status", newStatus).Error
			sub.Status = newStatus
		}
	}

	if shouldReactivateTenantAfterValidity(manuallySuspended, &tenant, effective) {
		if tenant.Status == database.TenantStatusSuspended {
			_ = database.CentralDB.Model(&tenant).Update("status", database.TenantStatusActive).Error
		}
	}

	saas.InvalidateTenantCache(sub.TenantID)

	meta, _ := json.Marshal(map[string]interface{}{
		"previous_end_date": oldEndStr,
		"new_end_date":      newEndStr,
		"effective_status":  effective,
		"ip":                clientIP,
	})
	sid := sub.ID
	actorID := saUserID
	saas.LogEvent(sub.TenantID, &sid, saas.EventValidityAdjusted, "admin", &actorID, reason, string(meta))

	payload, _ := json.Marshal(map[string]interface{}{
		"subscription_id":   sub.ID,
		"previous_end_date": oldEndStr,
		"new_end_date":      newEndStr,
		"reason":            reason,
		"effective_status":  effective,
	})
	database.WriteAuditLog(&database.AuditLog{
		TenantID:  sub.TenantID,
		UserID:    saUserID,
		Action:    "subscription_validity_adjusted",
		Entity:    "saas_subscription",
		EntityID:  sub.ID,
		Payload:   string(payload),
		IPAddress: clientIP,
	})

	return &sub, nil
}

// realignCurrentBillingCycle mueve al nuevo vencimiento el ciclo de cobro impago que cubre el
// período vigente de la suscripción.
//
// Se toca UN solo ciclo. Antes se actualizaban todos los impagos de la suscripción a la misma
// fecha, y eso rompía en cuanto había más de uno abierto —el del período actual y el del
// siguiente conviven mientras no se pague el primero—: los dos quedaban con el mismo
// period_end y MySQL rechazaba el lote por el índice único idx_billing_cycle_sub_period
// (error 1062), abortando el ajuste a medias.
func realignCurrentBillingCycle(tx *gorm.DB, subID uint, oldEnd, newEnd time.Time) error {
	var cycles []database.SaasBillingCycle
	if err := tx.Where("subscription_id = ?", subID).Order("due_date asc").Find(&cycles).Error; err != nil {
		return err
	}

	// El ciclo del período vigente es el que terminaba justo en el vencimiento anterior, y el
	// índice único (subscription_id, period_end) garantiza que sea uno solo. Se realinea aunque
	// esté PAGADO: EnsureBillingCycle decide si falta cobro comparando period_end == end_date
	// por igualdad exacta, así que dejar atrás el ciclo pagado hacía que el siguiente tick del
	// cron lo diera por inexistente y emitiera un cobro nuevo por un período ya cobrado —mover
	// la vigencia un día bastaba para facturar dos veces.
	//
	// Si las fechas ya venían desalineadas (ningún ciclo cierra en oldEnd) se cae al impago más
	// próximo a vencer, como antes. No hay que contemplar pending_review acá: AdjustValidity ya
	// bloqueó antes de llegar a esta función si la suscripción tiene algún ciclo en revisión
	// (ver el check al inicio de AdjustValidity).
	var target *database.SaasBillingCycle
	var fallback *database.SaasBillingCycle
	for i := range cycles {
		c := &cycles[i]
		// Los rechazados son cancelaciones administrativas: no cubren período alguno.
		if c.Status == database.SaasInvoiceRejected {
			continue
		}
		if c.PeriodEnd.Equal(oldEnd) {
			target = c
			break
		}
		if fallback == nil && (c.Status == database.SaasInvoicePending || c.Status == database.SaasInvoiceOverdue) {
			fallback = c
		}
	}
	if target == nil {
		target = fallback
	}
	if target == nil || target.PeriodEnd.Equal(newEnd) {
		return nil
	}

	// Un ciclo distinto ya cerrando en la fecha destino significaría cobrar dos veces ese
	// período. Es una decisión de negocio del operador (cancelar o ajustar ese cobro), no algo
	// que se pueda resolver solo.
	for i := range cycles {
		if cycles[i].ID != target.ID && cycles[i].PeriodEnd.Equal(newEnd) {
			return fmt.Errorf(
				"ya existe un ciclo de cobro de esta suscripción que termina el %s; cancélalo o ajústalo desde Cobros antes de mover la vigencia a esa fecha",
				newEnd.In(saas.LimaLocation()).Format("02/01/2006"))
		}
	}

	// En un ciclo ya pagado el due_date es historia —cuándo venció la deuda que el tenant saldó—,
	// así que solo se corrige el cierre del período. En los impagos ambos se mueven, como antes.
	fields := map[string]interface{}{"period_end": newEnd}
	if target.Status != database.SaasInvoicePaid {
		fields["due_date"] = newEnd
	}
	return tx.Model(&database.SaasBillingCycle{}).Where("id = ?", target.ID).
		Updates(fields).Error
}

func parseEndDateLima(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, errors.New("fecha de vencimiento requerida")
	}
	t, err := time.ParseInLocation("2006-01-02", s, saas.LimaLocation())
	if err != nil {
		return time.Time{}, errors.New("fecha de vencimiento inválida (use YYYY-MM-DD)")
	}
	return t, nil
}

func recalcStoredSubscriptionStatus(sub *database.SaasSubscription, effective string, now time.Time, graceDays int) string {
	if sub.Status == database.SaasSubTrial {
		switch effective {
		case database.SaasSubTrial, database.SaasSubActive, database.SaasSubGracePeriod, database.SaasSubProvisionalActive:
			return database.SaasSubTrial
		}
	}
	switch effective {
	case database.SaasSubActive, database.SaasSubTrial, database.SaasSubGracePeriod,
		database.SaasSubProvisionalActive, database.SaasSubOverdue:
		if sub.Status == database.SaasSubTrial {
			return database.SaasSubTrial
		}
		return database.SaasSubActive
	}
	if saas.CalendarDaysAfterEnd(sub.EndDate, now) > 0 {
		if graceDays <= 0 || saas.CalendarDaysAfterEnd(sub.EndDate, now) > graceDays {
			return database.SaasSubExpired
		}
	}
	return database.SaasSubActive
}

func shouldReactivateTenantAfterValidity(manuallySuspended bool, tenant *database.Tenant, effective string) bool {
	if manuallySuspended || tenant == nil {
		return false
	}
	if tenant.Status == "inactive" {
		return false
	}
	cfg, _ := saas.LoadSettings()
	if tenant.Status == database.TenantStatusBlocked || tenant.PaymentBlocked ||
		tenant.StrikeCount >= saas.EffectiveStrikeMax(cfg) {
		return false
	}
	switch effective {
	case database.SaasSubActive, database.SaasSubTrial, database.SaasSubGracePeriod, database.SaasSubProvisionalActive:
		return true
	}
	return false
}

func statusToLabel(status string) string {
	switch status {
	case database.SaasSubActive:
		return "Vigente"
	case database.SaasSubTrial:
		return "Prueba"
	case database.SaasSubGracePeriod:
		return "Período de gracia"
	case database.SaasSubOverdue:
		return "Mora"
	case database.SaasSubSuspended:
		return "Suspendido"
	case database.SaasSubCancelled:
		return "Cancelado"
	case database.SaasSubExpired:
		return "Expirado"
	case database.SaasSubProvisional:
		return "Provisional"
	case database.SaasSubProvisionalActive:
		return "Acceso provisional"
	default:
		return status
	}
}

func enrichSubscriptionDetail(detail *SubscriptionDetail) {
	detail.StatusLabel = statusToLabel(detail.Status)

	// Mora y gracia son del ciclo de cobro vigente: en una suscripción ya reemplazada solo
	// serían ruido («12 días de mora» en un histórico que nadie tiene que pagar).
	if !detail.IsCurrent {
		detail.DaysOverdue = 0
		detail.DaysInGrace = 0
		return
	}

	cfg, _ := saas.LoadSettings()
	now := saas.NowLima()
	daysAfter := saas.CalendarDaysAfterEnd(detail.EndDate, now)

	if daysAfter <= 0 {
		detail.DaysOverdue = 0
		detail.DaysInGrace = 0
	} else if daysAfter <= cfg.GracePeriodDays {
		detail.DaysOverdue = 0
		detail.DaysInGrace = cfg.GracePeriodDays - daysAfter
	} else {
		detail.DaysOverdue = daysAfter - cfg.GracePeriodDays
		detail.DaysInGrace = 0
	}
}
