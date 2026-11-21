package saas

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Reproduce del bug reportado en producción local: un tenant suspendido con un ciclo que
// incluye reconnection_fee>0 aprueba un pago que solo cubre cycle.Amount (sin el recargo).
func TestApprovePayment_rejectsPartialPayment_missingReconnectionFee(t *testing.T) {
	db := setupApprovePaymentDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly}
	db.Create(&plan)
	tenant := database.Tenant{Name: "DEMO", Slug: "demo", DBName: "demo", Status: database.TenantStatusSuspended}
	db.Create(&tenant)
	end := CalendarDateLima(time.Date(2026, 10, 22, 12, 0, 0, 0, lima()))
	sub := database.SaasSubscription{TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: database.SaasCycleMonthly, StartDate: end.AddDate(0, -1, 0), EndDate: EndOfDayLima(end), Status: database.SaasSubActive}
	db.Create(&sub)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: sub.ID, PlanID: plan.ID,
		PeriodStart: EndOfDayLima(end), PeriodEnd: EndOfDayLima(end.AddDate(0, 1, 0)),
		DueDate: EndOfDayLima(end), Amount: 99, ReconnectionFee: 50, Currency: "PEN", Status: database.SaasInvoicePending,
	}
	db.Create(&cycle)
	// Pasa por SubmitPayment (no arma el SaasPayment a mano): así también dispara la
	// reactivación provisional, que es justo lo que rompía el chequeo original al reponer
	// tenant.status="active" antes de que ApprovePayment pudiera leerlo como "suspended".
	pay, err := SubmitPayment(SubmitPaymentInput{
		TenantID: tenant.ID, BillingCycleID: cycle.ID, Amount: 99, PaymentMethod: "yape",
		ReceiptURL: "storage/saas/comprobante_test.png",
	})
	if err != nil {
		t.Fatalf("SubmitPayment: %v", err)
	}
	if pay.ReconnectionFee != 50 {
		t.Fatalf("payment.ReconnectionFee = %v, want 50 (tenant estaba suspendido al enviar)", pay.ReconnectionFee)
	}

	var reloaded database.Tenant
	db.First(&reloaded, tenant.ID)
	if reloaded.Status != database.TenantStatusActive {
		t.Fatalf("tenant.status tras SubmitPayment = %q, want active (reactivación provisional)", reloaded.Status)
	}

	if err := ApprovePayment(pay.ID, 0, 0, "test", 1, nil); err == nil {
		t.Fatal("se esperaba rechazo: el pago (99) no cubre base+reconexion (149)")
	}
}

// Excepción del panel central: un admin puede condonar o descontar el recargo de reconexión
// al aprobar, aunque el pago enviado no lo cubra por sí solo.
func TestApprovePayment_reconnectionFeeOverride(t *testing.T) {
	db := setupApprovePaymentDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly}
	db.Create(&plan)
	tenant := database.Tenant{Name: "DEMO", Slug: "demo", DBName: "demo", Status: database.TenantStatusSuspended}
	db.Create(&tenant)
	end := CalendarDateLima(time.Date(2026, 10, 22, 12, 0, 0, 0, lima()))
	sub := database.SaasSubscription{TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: database.SaasCycleMonthly, StartDate: end.AddDate(0, -1, 0), EndDate: EndOfDayLima(end), Status: database.SaasSubActive}
	db.Create(&sub)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: sub.ID, PlanID: plan.ID,
		PeriodStart: EndOfDayLima(end), PeriodEnd: EndOfDayLima(end.AddDate(0, 1, 0)),
		DueDate: EndOfDayLima(end), Amount: 99, ReconnectionFee: 50, Currency: "PEN", Status: database.SaasInvoicePending,
	}
	db.Create(&cycle)
	pay, err := SubmitPayment(SubmitPaymentInput{
		TenantID: tenant.ID, BillingCycleID: cycle.ID, Amount: 99, PaymentMethod: "yape",
		ReceiptURL: "storage/saas/comprobante_test.png",
	})
	if err != nil {
		t.Fatalf("SubmitPayment: %v", err)
	}

	// Sin condonar, 99 no alcanza (control ya cubierto por el test de arriba). Condonando el
	// recargo (override=0), el mismo pago de 99 sí debe alcanzar y aprobarse.
	waived := 0.0
	if err := ApprovePayment(pay.ID, 0, 0, "condonado por atención al cliente", 1, &waived); err != nil {
		t.Fatalf("se esperaba aprobar con recargo condonado: %v", err)
	}

	var approved database.SaasPayment
	if err := db.First(&approved, pay.ID).Error; err != nil {
		t.Fatalf("reload payment: %v", err)
	}
	if approved.Status != database.SaasPayApproved {
		t.Fatalf("payment.status = %q, want approved", approved.Status)
	}
	if approved.ReconnectionFee != 0 {
		t.Fatalf("payment.reconnection_fee tras condonar = %v, want 0 (debe quedar grabado lo que realmente se cobró)", approved.ReconnectionFee)
	}

	var approvedCycle database.SaasBillingCycle
	db.First(&approvedCycle, cycle.ID)
	if approvedCycle.Status != database.SaasInvoicePaid {
		t.Fatalf("cycle.status = %q, want paid", approvedCycle.Status)
	}
}
