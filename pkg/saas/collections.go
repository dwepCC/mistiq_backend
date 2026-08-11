package saas

import (
	"tukifac/pkg/database"
)

// CollectionAlert cobro que necesita atención del usuario central.
type CollectionAlert struct {
	TenantID       uint    `json:"tenant_id"`
	TenantName     string  `json:"tenant_name"`
	SubscriptionID uint    `json:"subscription_id,omitempty"`
	BillingCycleID uint    `json:"billing_cycle_id,omitempty"`
	PaymentID      uint    `json:"payment_id,omitempty"`
	PlanName       string  `json:"plan_name,omitempty"`
	Amount         float64 `json:"amount"`
	DueDate        string  `json:"due_date,omitempty"`
	// DaysOverdue días transcurridos desde que se agotó la ventana de pago.
	DaysOverdue int `json:"days_overdue,omitempty"`
	// NeverPaid el tenant nunca llegó a pagar ningún ciclo: es un alta que no se concretó,
	// no un cliente que dejó de pagar. Ventas lo trata distinto.
	NeverPaid bool `json:"never_paid,omitempty"`
	// TenantSuspended ya está suspendido; sirve para no volver a ofrecer la misma acción.
	TenantSuspended bool `json:"tenant_suspended,omitempty"`
}

// CollectionAlerts resumen de cobranza para la campana del panel central.
type CollectionAlerts struct {
	// PendingReview comprobantes que subieron los tenants y esperan validación.
	PendingReview []CollectionAlert `json:"pending_review"`
	// Overdue cobros que agotaron su ventana de pago sin pagarse.
	Overdue []CollectionAlert `json:"overdue"`
	// NeverPaidCount cuántos de los vencidos son altas que nunca pagaron nada.
	NeverPaidCount int `json:"never_paid_count"`
	Total          int `json:"total"`
}

// maxCollectionAlerts tope por grupo: la campana es un aviso, el detalle vive en /payments.
const maxCollectionAlerts = 50

// GetCollectionAlerts arma los dos grupos de la campana del panel central.
func GetCollectionAlerts() (CollectionAlerts, error) {
	out := CollectionAlerts{
		PendingReview: []CollectionAlert{},
		Overdue:       []CollectionAlert{},
	}
	if database.CentralDB == nil {
		return out, nil
	}

	var payments []database.SaasPayment
	database.CentralDB.Where("status = ?", database.SaasPayPendingReview).
		Order("created_at asc").Limit(maxCollectionAlerts).Find(&payments)

	var cycles []database.SaasBillingCycle
	database.CentralDB.Where("status = ?", database.SaasInvoiceOverdue).
		Order("due_date asc").Limit(maxCollectionAlerts).Find(&cycles)

	tenants := loadTenantsByID(collectTenantIDs(payments, cycles))
	plans := map[uint]string{}
	now := CalendarDateLima(NowLima())

	for _, p := range payments {
		t := tenants[p.TenantID]
		out.PendingReview = append(out.PendingReview, CollectionAlert{
			TenantID: p.TenantID, TenantName: tenantName(t), PaymentID: p.ID,
			Amount: p.Amount, TenantSuspended: isSuspended(t),
			BillingCycleID: derefUint(p.BillingCycleID),
		})
	}

	for i := range cycles {
		c := &cycles[i]
		t := tenants[c.TenantID]
		alert := CollectionAlert{
			TenantID: c.TenantID, TenantName: tenantName(t),
			SubscriptionID: c.SubscriptionID, BillingCycleID: c.ID,
			Amount:          c.Amount,
			DueDate:         c.DueDate.In(lima()).Format("2006-01-02"),
			DaysOverdue:     int(now.Sub(CalendarDateLima(c.DueDate)).Hours() / 24),
			NeverPaid:       tenantNeverPaid(c.TenantID),
			TenantSuspended: isSuspended(t),
			PlanName:        planNameCached(plans, c.PlanID),
		}
		if alert.NeverPaid {
			out.NeverPaidCount++
		}
		out.Overdue = append(out.Overdue, alert)
	}

	out.Total = len(out.PendingReview) + len(out.Overdue)
	return out, nil
}

// tenantNeverPaid: el tenant no tiene ningún ciclo pagado, así que nunca llegó a ser cliente
// activo de pago. Distinto de quien pagó meses y ahora se atrasó.
func tenantNeverPaid(tenantID uint) bool {
	var paid int64
	database.CentralDB.Model(&database.SaasBillingCycle{}).
		Where("tenant_id = ? AND status = ?", tenantID, database.SaasInvoicePaid).
		Count(&paid)
	return paid == 0
}

func collectTenantIDs(payments []database.SaasPayment, cycles []database.SaasBillingCycle) []uint {
	seen := map[uint]bool{}
	ids := make([]uint, 0, len(payments)+len(cycles))
	add := func(id uint) {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, p := range payments {
		add(p.TenantID)
	}
	for _, c := range cycles {
		add(c.TenantID)
	}
	return ids
}

func loadTenantsByID(ids []uint) map[uint]*database.Tenant {
	out := map[uint]*database.Tenant{}
	if len(ids) == 0 {
		return out
	}
	var rows []database.Tenant
	database.CentralDB.Select("id", "name", "status").Where("id IN ?", ids).Find(&rows)
	for i := range rows {
		out[rows[i].ID] = &rows[i]
	}
	return out
}

func planNameCached(cache map[uint]string, planID uint) string {
	if planID == 0 {
		return ""
	}
	if name, ok := cache[planID]; ok {
		return name
	}
	var plan database.SaasPlan
	if database.CentralDB.Select("id", "name").First(&plan, planID).Error != nil {
		cache[planID] = ""
		return ""
	}
	cache[planID] = plan.Name
	return plan.Name
}

func tenantName(t *database.Tenant) string {
	if t == nil {
		return ""
	}
	return t.Name
}

func isSuspended(t *database.Tenant) bool {
	return t != nil && (t.Status == database.TenantStatusSuspended || t.Status == database.TenantStatusBlocked)
}

func derefUint(v *uint) uint {
	if v == nil {
		return 0
	}
	return *v
}
