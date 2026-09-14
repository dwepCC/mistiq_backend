package saas

import (
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/saas/pricing"
	"tukifac/pkg/tenantcache"
)

// TenantSubscriptionView estado resumido para tenant UI y middleware.
type TenantSubscriptionView struct {
	HasSubscription         bool    `json:"has_subscription"`
	SubscriptionID          uint    `json:"subscription_id,omitempty"`
	PlanID                  uint    `json:"plan_id,omitempty"`
	PlanName                string  `json:"plan_name"`
	BillingCycle            string  `json:"billing_cycle"`
	Status                  string  `json:"status"`
	TenantStatus            string  `json:"tenant_status"`
	StartDate               string  `json:"start_date,omitempty"`
	EndDate                 string  `json:"end_date,omitempty"`
	DaysUntilExpiry         int     `json:"days_until_expiry"`
	InGracePeriod           bool    `json:"in_grace_period"`
	IsOverdue               bool    `json:"is_overdue"`
	IsSuspended             bool    `json:"is_suspended"`
	IsBlocked               bool    `json:"is_blocked"`
	StrikeCount             int     `json:"strike_count"`
	CanSubmitPayment        bool    `json:"can_submit_payment"`
	ProvisionalUntil        string  `json:"provisional_until,omitempty"`
	PendingAmount           float64 `json:"pending_amount"`
	ReconnectionFee         float64 `json:"reconnection_fee"`
	ShowRenewalBanner       bool    `json:"show_renewal_banner"`
	ShowSuspendedBanner     bool    `json:"show_suspended_banner"`
	CanOperate              bool    `json:"can_operate"`
	PortalURL               string  `json:"portal_url"` // override opcional; vacío = usar /subscription
	NextBillingDate         string  `json:"next_billing_date,omitempty"`
	PendingInvoiceID        uint    `json:"pending_invoice_id,omitempty"`
	SupportMessage          string  `json:"support_message,omitempty"`
	HasPendingPaymentReview bool    `json:"has_pending_payment_review"`
	ProvisionalHoursLeft    int     `json:"provisional_hours_left"`
	// ContractedMonths meses realmente contratados (start_date → end_date). billing_cycle
	// viene del plan, así que un plan mensual contratado por 3 meses seguía diciendo
	// «Mensual»; este campo permite mostrar el período que de verdad se pagó.
	ContractedMonths int `json:"contracted_months"`
}

// ContractedMonths meses completos entre inicio y fin de la suscripción.
//
// Se cuenta por calendario, no por días: de 13/07 a 13/09 son 2 meses aunque sumen 62
// días. Un resto de días cuenta como mes empezado (13/07 → 15/09 = 3), porque para el
// usuario ese tramo también está cubierto.
func ContractedMonths(start, end time.Time) int {
	return pricing.MonthsBetween(start, end, lima())
}

// CycleMonthsFromBilling devuelve meses por ciclo.
func CycleMonthsFromBilling(cycle string) int {
	switch cycle {
	case database.SaasCycleSemiannual:
		return 6
	case database.SaasCycleAnnual, "yearly":
		return 12
	case "lifetime":
		return 1200
	default:
		return 1
	}
}

// GetTenantView estado efectivo del tenant. Cachea el resultado una ventana breve
// (tenantViewCacheTTL) para no repetir ~8 consultas a la BD central por cada request ERP.
// La caché se invalida al instante en cambios vía InvalidateTenantCache.
func GetTenantView(tenantID uint) (TenantSubscriptionView, error) {
	if v, ok := getCachedTenantView(tenantID); ok {
		return v, nil
	}
	v, err := computeTenantView(tenantID)
	if err == nil {
		setCachedTenantView(tenantID, v)
	}
	return v, err
}

// computeTenantView calcula estado efectivo desde BD central (fechas en America/Lima).
func computeTenantView(tenantID uint) (TenantSubscriptionView, error) {
	cfg, _ := LoadSettings()
	strikeMax := EffectiveStrikeMax(cfg)
	v := TenantSubscriptionView{
		PortalURL:        strings.TrimSpace(cfg.PortalURLOverride),
		ReconnectionFee:  cfg.ReconnectionFee,
		CanSubmitPayment: true,
	}

	var tenant database.Tenant
	if err := database.CentralDB.First(&tenant, tenantID).Error; err != nil {
		return v, err
	}
	v.TenantStatus = tenant.Status
	v.StrikeCount = tenant.StrikeCount
	v.IsBlocked = tenant.Status == database.TenantStatusBlocked || tenant.PaymentBlocked || tenant.StrikeCount >= strikeMax
	if v.IsBlocked {
		v.CanSubmitPayment = false
		v.SupportMessage = "Cuenta bloqueada por comprobantes inválidos repetidos. Contacte a soporte o ventas."
	}

	var sub database.SaasSubscription
	err := database.CentralDB.Where("tenant_id = ?", tenantID).
		Where("status NOT IN ?", []string{database.SaasSubCancelled}).
		Order("created_at desc").First(&sub).Error
	if err != nil {
		v.CanOperate = !v.IsBlocked && tenant.Status != database.TenantStatusSuspended
		return v, nil
	}

	v.HasSubscription = true
	v.SubscriptionID = sub.ID
	v.PlanID = sub.PlanID
	v.BillingCycle = sub.BillingCycle
	v.StartDate = sub.StartDate.In(lima()).Format(timeRFC3339Lima)
	v.EndDate = sub.EndDate.In(lima()).Format(timeRFC3339Lima)
	v.ContractedMonths = ContractedMonths(sub.StartDate, sub.EndDate)

	now := NowLima()
	if sub.ProvisionalUntil != nil {
		v.ProvisionalUntil = sub.ProvisionalUntil.In(lima()).Format(timeRFC3339Lima)
		if sub.ProvisionalUntil.After(now) {
			v.ProvisionalHoursLeft = int(sub.ProvisionalUntil.Sub(now).Hours())
			if v.ProvisionalHoursLeft < 1 {
				v.ProvisionalHoursLeft = 1
			}
		}
	}

	var pendingPay int64
	database.CentralDB.Model(&database.SaasPayment{}).
		Where("tenant_id = ? AND status = ?", tenantID, database.SaasPayPendingReview).
		Count(&pendingPay)
	v.HasPendingPaymentReview = pendingPay > 0

	var plan database.SaasPlan
	database.CentralDB.First(&plan, sub.PlanID)
	v.PlanName = plan.Name
	v.DaysUntilExpiry = CalendarDaysUntilEnd(sub.EndDate, now)

	effective := ResolveEffectiveStatus(&sub, &tenant, now, cfg.GracePeriodDays)
	v.Status = effective
	v.InGracePeriod = effective == database.SaasSubGracePeriod
	v.IsOverdue = effective == database.SaasSubOverdue
	v.IsSuspended = effective == database.SaasSubSuspended ||
		tenant.Status == database.TenantStatusSuspended ||
		(effective == database.SaasSubOverdue && tenant.Status != database.TenantStatusActive)

	// Incluye pending_review a propósito: el monto sigue sin pagarse mientras se revisa el
	// comprobante, solo cambia cómo se lo comunica el UX (ver billingDebtApplies/resolvePaymentUX,
	// que sí distinguen "en revisión" de "hay que pagar ya"). Si se excluyera acá, un tenant
	// suspendido que acaba de subir su comprobante vería "deuda: S/ 0" mientras espera revisión.
	var pending database.SaasBillingCycle
	if database.CentralDB.Where("tenant_id = ? AND status IN ?", tenantID,
		[]string{database.SaasInvoicePending, database.SaasInvoiceOverdue, database.SaasInvoicePendingReview}).
		Order("due_date asc").First(&pending).Error == nil {
		v.PendingAmount = BillingCycleAmountDue(&pending, &tenant, &sub)
		v.PendingInvoiceID = pending.ID
	}

	v.ShowRenewalBanner = ShouldShowRenewalBanner(v.DaysUntilExpiry, effective, cfg.ReminderDays)
	v.ShowSuspendedBanner = v.IsBlocked || v.IsSuspended || v.IsOverdue ||
		(!v.CanOperate && !v.IsBlocked)

	v.CanOperate = CanOperate(effective, &tenant, sub.ProvisionalUntil, now)
	return v, nil
}

const timeRFC3339Lima = "2006-01-02T15:04:05-07:00"

// CanOperate reglas de acceso operativo (ventas, POS, inventario, facturación SUNAT).
func CanOperate(subStatus string, tenant *database.Tenant, provisionalUntil *time.Time, now time.Time) bool {
	if tenant == nil {
		return false
	}
	cfgStrike, _ := LoadSettings()
	maxStrike := EffectiveStrikeMax(cfgStrike)
	if tenant.Status == database.TenantStatusBlocked || tenant.PaymentBlocked || tenant.StrikeCount >= maxStrike {
		return false
	}
	if provisionalUntil != nil && provisionalUntil.In(lima()).After(now) {
		return true
	}
	if tenant.Status == database.TenantStatusSuspended {
		return false
	}
	switch subStatus {
	case database.SaasSubActive, database.SaasSubTrial, database.SaasSubGracePeriod,
		database.SaasSubProvisional, database.SaasSubProvisionalActive:
		return true
	default:
		return false
	}
}

// ResolveEffectiveStatus por día calendario Lima (no timestamp exacto).
// Si vence hoy: activo todo el día hasta 23:59.
func ResolveEffectiveStatus(sub *database.SaasSubscription, tenant *database.Tenant, now time.Time, graceDays int) string {
	if sub == nil {
		return ""
	}
	if tenant != nil {
		cfg, _ := LoadSettings()
		maxStrike := EffectiveStrikeMax(cfg)
		if tenant.Status == database.TenantStatusBlocked || tenant.StrikeCount >= maxStrike {
			return database.TenantStatusBlocked
		}
	}
	if sub.ProvisionalUntil != nil && sub.ProvisionalUntil.In(lima()).After(now) {
		return database.SaasSubProvisionalActive
	}
	if sub.Status == database.SaasSubSuspended {
		return database.SaasSubSuspended
	}
	if sub.Status == database.SaasSubCancelled {
		return database.SaasSubCancelled
	}
	if sub.Status == database.SaasSubProvisionalActive {
		return database.SaasSubProvisionalActive
	}

	daysAfter := CalendarDaysAfterEnd(sub.EndDate, now)
	if daysAfter <= 0 {
		if sub.Status == database.SaasSubTrial {
			return database.SaasSubTrial
		}
		return database.SaasSubActive
	}
	if graceDays <= 0 {
		return database.SaasSubOverdue
	}
	if daysAfter <= graceDays {
		return database.SaasSubGracePeriod
	}
	return database.SaasSubOverdue
}

// InvalidateTenantCache tras cambios de suscripción/pago.
func InvalidateTenantCache(tenantID uint) {
	clearCachedTenantView(tenantID)
	var t database.Tenant
	if database.CentralDB.First(&t, tenantID).Error == nil {
		tenantcache.Invalidate(t.Slug)
	}
}

// LogEvent auditoría central.
func LogEvent(tenantID uint, subID *uint, eventType, actorType string, actorID *uint, reason, meta string) {
	_ = database.CentralDB.Create(&database.SaasSubscriptionEvent{
		TenantID:       tenantID,
		SubscriptionID: subID,
		EventType:      eventType,
		ActorType:      actorType,
		ActorID:        actorID,
		Reason:         reason,
		MetadataJSON:   meta,
	}).Error
}
