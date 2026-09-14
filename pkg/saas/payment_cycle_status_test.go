package saas

import (
	"testing"

	"tukifac/pkg/database"
)

// Subir un comprobante debe mover el ciclo a pending_review — es lo que hace que
// /subscription/summary deje de ofrecer "Pagar ahora" mientras se revisa (ver
// subscriptionUx.ts en ambos frontends tenant, que ya solo miran el status real del ciclo).
func TestSubmitPayment_movesCycleToPendingReview(t *testing.T) {
	db := setupApprovePaymentDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: 1, PlanID: 1,
		PeriodStart: NowLima(), PeriodEnd: NowLima().AddDate(0, 1, 0), DueDate: NowLima(),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoicePending,
	}
	db.Create(&cycle)

	_, err := SubmitPayment(SubmitPaymentInput{
		TenantID: tenant.ID, BillingCycleID: cycle.ID, Amount: 99,
		PaymentMethod: "transfer", ReceiptURL: "/x.jpg",
	})
	if err != nil {
		t.Fatalf("SubmitPayment: %v", err)
	}

	var after database.SaasBillingCycle
	db.First(&after, cycle.ID)
	if after.Status != database.SaasInvoicePendingReview {
		t.Errorf("ciclo status = %q, se esperaba pending_review", after.Status)
	}
}

// Un ciclo ya vencido (overdue) también debe aceptar el comprobante y pasar a pending_review:
// el cron no debe volver a tocarlo mientras se revisa (ver RunLimaDailyEvaluation, que solo
// barre 'pending').
func TestSubmitPayment_overdueCycleMovesToPendingReview(t *testing.T) {
	db := setupApprovePaymentDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: 1, PlanID: 1,
		PeriodStart: NowLima().AddDate(0, 0, -30), PeriodEnd: NowLima(), DueDate: NowLima().AddDate(0, 0, -30),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoiceOverdue,
	}
	db.Create(&cycle)

	if _, err := SubmitPayment(SubmitPaymentInput{
		TenantID: tenant.ID, BillingCycleID: cycle.ID, Amount: 99,
		PaymentMethod: "transfer", ReceiptURL: "/x.jpg",
	}); err != nil {
		t.Fatalf("SubmitPayment: %v", err)
	}

	var after database.SaasBillingCycle
	db.First(&after, cycle.ID)
	if after.Status != database.SaasInvoicePendingReview {
		t.Errorf("ciclo status = %q, se esperaba pending_review", after.Status)
	}
}

// No se puede subir un comprobante para un ciclo que ya está pagado.
func TestSubmitPayment_rejectsAlreadyPaidCycle(t *testing.T) {
	db := setupApprovePaymentDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: 1, PlanID: 1,
		PeriodStart: NowLima(), PeriodEnd: NowLima().AddDate(0, 1, 0), DueDate: NowLima(),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoicePaid,
	}
	db.Create(&cycle)

	if _, err := SubmitPayment(SubmitPaymentInput{
		TenantID: tenant.ID, BillingCycleID: cycle.ID, Amount: 99,
		PaymentMethod: "transfer", ReceiptURL: "/x.jpg",
	}); err == nil {
		t.Error("se esperaba error al subir un comprobante contra un ciclo ya pagado")
	}
}

// Reenvío: si el tenant vuelve a subir un comprobante para el mismo ciclo (ya en
// pending_review), se acepta — el anterior queda superado (supersedePriorPendingPayments) y el
// ciclo se queda en pending_review (idempotente, no hay error por "estado repetido").
func TestSubmitPayment_resubmissionOnCycleAlreadyInReview(t *testing.T) {
	db := setupApprovePaymentDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: 1, PlanID: 1,
		PeriodStart: NowLima(), PeriodEnd: NowLima().AddDate(0, 1, 0), DueDate: NowLima(),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoicePendingReview,
	}
	db.Create(&cycle)
	primero := database.SaasPayment{
		TenantID: tenant.ID, BillingCycleID: &cycle.ID, Amount: 99, Status: database.SaasPayPendingReview,
	}
	db.Create(&primero)

	segundo, err := SubmitPayment(SubmitPaymentInput{
		TenantID: tenant.ID, BillingCycleID: cycle.ID, Amount: 99,
		PaymentMethod: "transfer", ReceiptURL: "/y.jpg",
	})
	if err != nil {
		t.Fatalf("SubmitPayment (reenvío): %v", err)
	}

	var after database.SaasBillingCycle
	db.First(&after, cycle.ID)
	if after.Status != database.SaasInvoicePendingReview {
		t.Errorf("ciclo status = %q, se esperaba seguir en pending_review", after.Status)
	}
	var reloadedPrimero database.SaasPayment
	db.First(&reloadedPrimero, primero.ID)
	if reloadedPrimero.Status != database.SaasPayRejected {
		t.Errorf("el comprobante anterior debía quedar superado (rejected), quedó %q", reloadedPrimero.Status)
	}
	if segundo.Status != database.SaasPayPendingReview {
		t.Errorf("el comprobante nuevo debía quedar pending_review, quedó %q", segundo.Status)
	}
}

// Rechazar el pago cuyo plazo sigue vigente devuelve el ciclo a 'pending' — reaparece el botón
// "Pagar ahora" en el panel del tenant.
func TestRejectPayment_revertsCycleToPendingWhenStillWithinWindow(t *testing.T) {
	db := setupApprovePaymentDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	// Vence dentro de 10 días: muy por delante de la ventana de pago (default 3 días), sigue
	// siendo 'pending' tras el rechazo, no 'overdue'.
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: 1, PlanID: 1,
		PeriodStart: NowLima(), PeriodEnd: NowLima().AddDate(0, 1, 0), DueDate: NowLima().AddDate(0, 0, 10),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoicePendingReview,
	}
	db.Create(&cycle)
	pago := database.SaasPayment{
		TenantID: tenant.ID, BillingCycleID: &cycle.ID, Amount: 99, Status: database.SaasPayPendingReview,
	}
	db.Create(&pago)

	if err := RejectPayment(pago.ID, "comprobante ilegible", 1); err != nil {
		t.Fatalf("RejectPayment: %v", err)
	}

	var after database.SaasBillingCycle
	db.First(&after, cycle.ID)
	if after.Status != database.SaasInvoicePending {
		t.Errorf("ciclo status = %q, se esperaba pending (plazo aún vigente)", after.Status)
	}
}

// Rechazar el pago cuyo plazo ya se agotó devuelve el ciclo a 'overdue' — mismo criterio que
// usaría el cron si nunca hubiera habido un comprobante.
func TestRejectPayment_revertsCycleToOverdueWhenWindowExpired(t *testing.T) {
	db := setupApprovePaymentDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	// Venció hace 30 días: muy por detrás de la ventana de pago (default 3 días).
	due := NowLima().AddDate(0, 0, -30)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: 1, PlanID: 1,
		PeriodStart: due.AddDate(0, -1, 0), PeriodEnd: due, DueDate: due,
		Amount: 99, Currency: "PEN", Status: database.SaasInvoicePendingReview,
	}
	db.Create(&cycle)
	pago := database.SaasPayment{
		TenantID: tenant.ID, BillingCycleID: &cycle.ID, Amount: 99, Status: database.SaasPayPendingReview,
	}
	db.Create(&pago)

	if err := RejectPayment(pago.ID, "comprobante ilegible", 1); err != nil {
		t.Fatalf("RejectPayment: %v", err)
	}

	var after database.SaasBillingCycle
	db.First(&after, cycle.ID)
	if after.Status != database.SaasInvoiceOverdue {
		t.Errorf("ciclo status = %q, se esperaba overdue (plazo ya agotado)", after.Status)
	}
}

// Una solicitud de renovación (SubmitRenewalRequest / "elegir plan", sin billing_cycle_id) no
// debía apilarse sin fin: al llegar una nueva, la anterior queda superada (rejected) — mismo
// criterio que ya existía para pagos ligados a un ciclo, pero agrupando por tenant en vez de
// billing_cycle_id (acá no hay ninguno).
func TestSubmitPayment_supersedesResubmittedRenewalRequestWithoutCycle(t *testing.T) {
	db := setupApprovePaymentDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	plan := database.SaasPlan{Name: "Pro", Price: 49, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)

	primera, err := SubmitRenewalRequest(SubmitRenewalRequestInput{
		TenantID: tenant.ID, PlanID: plan.ID, PeriodMonths: 1, PaymentMethod: "yape", ReceiptURL: "/a.jpg",
	})
	if err != nil {
		t.Fatalf("primera solicitud: %v", err)
	}

	segunda, err := SubmitRenewalRequest(SubmitRenewalRequestInput{
		TenantID: tenant.ID, PlanID: plan.ID, PeriodMonths: 1, PaymentMethod: "yape", ReceiptURL: "/b.jpg",
	})
	if err != nil {
		t.Fatalf("segunda solicitud: %v", err)
	}

	var reloadedPrimera database.SaasPayment
	db.First(&reloadedPrimera, primera.ID)
	if reloadedPrimera.Status != database.SaasPayRejected {
		t.Errorf("primera solicitud = %q, se esperaba rejected (superada)", reloadedPrimera.Status)
	}
	var reloadedSegunda database.SaasPayment
	db.First(&reloadedSegunda, segunda.ID)
	if reloadedSegunda.Status != database.SaasPayPendingReview {
		t.Errorf("segunda solicitud = %q, se esperaba pending_review", reloadedSegunda.Status)
	}

	// Y con esto, has_pending_payment_review ya no cuenta la primera (superada) como viva.
	var pendingCount int64
	db.Model(&database.SaasPayment{}).
		Where("tenant_id = ? AND status = ?", tenant.ID, database.SaasPayPendingReview).
		Count(&pendingCount)
	if pendingCount != 1 {
		t.Errorf("hay %d pagos pending_review, se esperaba 1 (solo la segunda solicitud)", pendingCount)
	}
}

// Antes de este fix, aprobar un pago sin billing_cycle_id (solicitud de renovación) dejaba el
// enlace en un solo sentido: el ciclo nuevo sabía qué pago lo saldó (cycle.payment_id), pero el
// pago se quedaba con billing_cycle_id NULL para siempre — rompía, entre otras cosas, el
// comprobante del tenant en InvoiceView.ReceiptURL (que busca por billing_cycle_id).
func TestApprovePayment_backfillsBillingCycleIDForRenewalRequestWithoutCycle(t *testing.T) {
	db := setupApprovePaymentDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	plan := database.SaasPlan{Name: "Pro", Price: 49, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)

	payment, err := SubmitRenewalRequest(SubmitRenewalRequestInput{
		TenantID: tenant.ID, PlanID: plan.ID, PeriodMonths: 1, PaymentMethod: "yape", ReceiptURL: "/x.jpg",
	})
	if err != nil {
		t.Fatalf("solicitud: %v", err)
	}
	if payment.BillingCycleID != nil {
		t.Fatalf("precondición: el pago no debía tener billing_cycle_id todavía")
	}

	if err := ApprovePayment(payment.ID, plan.ID, 1, "ok", 1); err != nil {
		t.Fatalf("ApprovePayment: %v", err)
	}

	var reloaded database.SaasPayment
	db.First(&reloaded, payment.ID)
	if reloaded.BillingCycleID == nil {
		t.Fatal("billing_cycle_id sigue NULL tras aprobar; el enlace pago→ciclo no quedó")
	}

	var cycle database.SaasBillingCycle
	db.First(&cycle, *reloaded.BillingCycleID)
	if cycle.PaymentID == nil || *cycle.PaymentID != payment.ID {
		t.Errorf("el ciclo #%d no apunta de vuelta a este pago (payment_id=%v)", cycle.ID, cycle.PaymentID)
	}

	// Con el enlace ya bidireccional, el comprobante del tenant se ve en la factura del período.
	invoices := ListInvoicesView(tenant.ID)
	if len(invoices) != 1 || invoices[0].ReceiptURL != "/x.jpg" {
		t.Errorf("ReceiptURL de la factura = %q, se esperaba /x.jpg", invoices[0].ReceiptURL)
	}
}

// Rechazar un pago que ya no aplica (el ciclo lo pagó otro pago mientras tanto) es limpieza
// administrativa: no debe tocar el ciclo, que sigue 'paid'.
func TestRejectPayment_doesNotRevertAlreadyPaidCycle(t *testing.T) {
	db := setupApprovePaymentDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	otroPagoID := uint(999)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: 1, PlanID: 1,
		PeriodStart: NowLima(), PeriodEnd: NowLima().AddDate(0, 1, 0), DueDate: NowLima(),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoicePaid, PaymentID: &otroPagoID,
	}
	db.Create(&cycle)
	pago := database.SaasPayment{
		TenantID: tenant.ID, BillingCycleID: &cycle.ID, Amount: 99, Status: database.SaasPayPendingReview,
	}
	db.Create(&pago)

	if err := RejectPayment(pago.ID, "sobrante, ya se pagó con otro comprobante", 1); err != nil {
		t.Fatalf("RejectPayment: %v", err)
	}

	var after database.SaasBillingCycle
	db.First(&after, cycle.ID)
	if after.Status != database.SaasInvoicePaid {
		t.Errorf("ciclo status = %q, no debía tocarse (seguía pagado por otro pago)", after.Status)
	}
}
