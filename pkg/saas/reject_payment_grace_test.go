package saas

import (
	"testing"

	"tukifac/pkg/database"
)

// Rechazar un comprobante mientras la suscripción sigue dentro de su ventana de gracia por
// calendario (grace_period_days configurado en la central, default 3) NO debe suspenderla: para
// eso existe la tolerancia. El strike se cuenta igual, pero el acceso se mantiene.
func TestRejectPayment_withinGrace_doesNotSuspend(t *testing.T) {
	db := setupApprovePaymentDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	// Venció ayer: con gracia default de 3 días, sigue dentro de la ventana.
	endDate := NowLima().AddDate(0, 0, -1)
	provisionalUntil := NowLima().AddDate(0, 0, 1)
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: database.SaasCycleMonthly,
		EndDate: endDate, Status: database.SaasSubProvisionalActive, ProvisionalUntil: &provisionalUntil,
	}
	db.Create(&sub)

	payment := database.SaasPayment{
		TenantID: tenant.ID, SubscriptionID: &sub.ID, Amount: 99, Currency: "PEN",
		Status: database.SaasPayPendingReview, ProvisionalApplied: true,
	}
	db.Create(&payment)

	if err := RejectPayment(payment.ID, "comprobante ilegible", 1); err != nil {
		t.Fatalf("RejectPayment: %v", err)
	}

	var reloadedTenant database.Tenant
	db.First(&reloadedTenant, tenant.ID)
	if reloadedTenant.Status == database.TenantStatusSuspended {
		t.Errorf("tenant no debe quedar suspendido dentro de gracia, quedó %q", reloadedTenant.Status)
	}
	if reloadedTenant.StrikeCount != 1 {
		t.Errorf("strike_count = %d, want 1 (el strike se cuenta igual)", reloadedTenant.StrikeCount)
	}

	var reloadedSub database.SaasSubscription
	db.First(&reloadedSub, sub.ID)
	if reloadedSub.Status == database.SaasSubSuspended {
		t.Errorf("suscripción no debe quedar suspendida dentro de gracia, quedó %q", reloadedSub.Status)
	}
	if reloadedSub.ProvisionalUntil != nil {
		t.Errorf("provisional_until debe limpiarse (ya no aplica, el pago que lo dio fue rechazado)")
	}

	var reloadedPayment database.SaasPayment
	db.First(&reloadedPayment, payment.ID)
	if reloadedPayment.Status != database.SaasPayRejected {
		t.Errorf("payment status = %q, want rejected", reloadedPayment.Status)
	}
}

// Fuera de gracia, el comportamiento es el de siempre: rechazar revierte el provisional y
// suspende (el provisional era lo único que sostenía el acceso).
func TestRejectPayment_outsideGrace_suspends(t *testing.T) {
	db := setupApprovePaymentDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	// Venció hace 10 días: supera la gracia default de 3 días.
	endDate := NowLima().AddDate(0, 0, -10)
	provisionalUntil := NowLima().AddDate(0, 0, 1)
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: database.SaasCycleMonthly,
		EndDate: endDate, Status: database.SaasSubProvisionalActive, ProvisionalUntil: &provisionalUntil,
	}
	db.Create(&sub)

	payment := database.SaasPayment{
		TenantID: tenant.ID, SubscriptionID: &sub.ID, Amount: 99, Currency: "PEN",
		Status: database.SaasPayPendingReview, ProvisionalApplied: true,
	}
	db.Create(&payment)

	if err := RejectPayment(payment.ID, "comprobante inválido", 1); err != nil {
		t.Fatalf("RejectPayment: %v", err)
	}

	var reloadedTenant database.Tenant
	db.First(&reloadedTenant, tenant.ID)
	if reloadedTenant.Status != database.TenantStatusSuspended {
		t.Errorf("tenant status = %q, want suspended (fuera de gracia)", reloadedTenant.Status)
	}

	var reloadedSub database.SaasSubscription
	db.First(&reloadedSub, sub.ID)
	if reloadedSub.Status != database.SaasSubSuspended {
		t.Errorf("sub status = %q, want suspended (fuera de gracia)", reloadedSub.Status)
	}
	if reloadedSub.ProvisionalUntil != nil {
		t.Errorf("provisional_until debe limpiarse")
	}
}

// Reincidencia (llega a strike_max) bloquea aunque el calendario todavía ayude: es una medida
// de abuso, no de mora, y no debería poder evadirse subiendo comprobantes basura mientras se
// está en gracia.
func TestRejectPayment_strikeMaxReached_blocksEvenWithinGrace(t *testing.T) {
	db := setupApprovePaymentDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)
	// strike_count ya en 1: el próximo rechazo llega a maxStrike (default 2).
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive, StrikeCount: 1}
	db.Create(&tenant)

	endDate := NowLima().AddDate(0, 0, -1) // dentro de gracia
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: database.SaasCycleMonthly,
		EndDate: endDate, Status: database.SaasSubActive,
	}
	db.Create(&sub)

	payment := database.SaasPayment{
		TenantID: tenant.ID, SubscriptionID: &sub.ID, Amount: 99, Currency: "PEN",
		Status: database.SaasPayPendingReview,
	}
	db.Create(&payment)

	if err := RejectPayment(payment.ID, "comprobante falso", 1); err != nil {
		t.Fatalf("RejectPayment: %v", err)
	}

	var reloadedTenant database.Tenant
	db.First(&reloadedTenant, tenant.ID)
	if reloadedTenant.Status != database.TenantStatusBlocked || !reloadedTenant.PaymentBlocked {
		t.Errorf("tenant debe quedar bloqueado por reincidencia aunque esté en gracia, quedó status=%q payment_blocked=%v",
			reloadedTenant.Status, reloadedTenant.PaymentBlocked)
	}
}
