package saas

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Caso normal: pago contra un ciclo ya emitido. Revertir debe devolver la suscripción a su
// end_date de antes, el ciclo a 'pending' (sin paid_at ni payment_id), y el pago a 'reversed'.
func TestRevertApprovedPayment_cycleLinked_restoresPriorState(t *testing.T) {
	db := setupApprovePaymentDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly}
	db.Create(&plan)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	curEnd := CalendarDateLima(time.Date(2026, 8, 24, 12, 0, 0, 0, lima()))
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: database.SaasCycleMonthly,
		StartDate: curEnd.AddDate(0, -1, 0), EndDate: EndOfDayLima(curEnd), Status: database.SaasSubActive,
		BilledMonths: 1,
	}
	db.Create(&sub)

	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: sub.ID, PlanID: plan.ID,
		PeriodStart: EndOfDayLima(curEnd.AddDate(0, -1, 0)), PeriodEnd: EndOfDayLima(curEnd),
		DueDate: EndOfDayLima(curEnd), Amount: 99, Currency: "PEN", Status: database.SaasInvoicePending,
	}
	db.Create(&cycle)

	pay := database.SaasPayment{
		TenantID: tenant.ID, BillingCycleID: &cycle.ID, Amount: 99, Currency: "PEN",
		Status: database.SaasPayPendingReview,
	}
	db.Create(&pay)

	if err := ApprovePayment(pay.ID, 0, 0, "ok", 1); err != nil {
		t.Fatalf("ApprovePayment: %v", err)
	}
	var afterApprove database.SaasPayment
	db.First(&afterApprove, pay.ID)
	if afterApprove.PreApprovalSnapshotJSON == "" {
		t.Fatalf("ApprovePayment no guardó el snapshot de reversión")
	}

	if err := RevertApprovedPayment(pay.ID, "prueba: anular renovación duplicada", 1); err != nil {
		t.Fatalf("RevertApprovedPayment: %v", err)
	}

	var reloadedSub database.SaasSubscription
	db.First(&reloadedSub, sub.ID)
	if got, want := CalendarDateLima(reloadedSub.EndDate), curEnd; !got.Equal(want) {
		t.Errorf("EndDate tras revertir = %s, want %s (el de antes de aprobar)", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
	if reloadedSub.Status != database.SaasSubActive {
		t.Errorf("status tras revertir = %q, want active (el de antes)", reloadedSub.Status)
	}

	var reloadedCycle database.SaasBillingCycle
	db.First(&reloadedCycle, cycle.ID)
	if reloadedCycle.Status != database.SaasInvoicePending {
		t.Errorf("ciclo status tras revertir = %q, want pending", reloadedCycle.Status)
	}
	if reloadedCycle.PaymentID != nil {
		t.Errorf("ciclo payment_id tras revertir = %v, want nil", reloadedCycle.PaymentID)
	}
	if reloadedCycle.PaidAt != nil {
		t.Errorf("ciclo paid_at tras revertir = %v, want nil", reloadedCycle.PaidAt)
	}

	var reloadedPay database.SaasPayment
	db.First(&reloadedPay, pay.ID)
	if reloadedPay.Status != database.SaasPayReversed {
		t.Errorf("pago status tras revertir = %q, want reversed", reloadedPay.Status)
	}
	if reloadedPay.ReversedBy == nil || *reloadedPay.ReversedBy != 1 {
		t.Errorf("reversed_by = %v, want 1", reloadedPay.ReversedBy)
	}
}

// El caso real que motivó esto: el tenant renueva desde su plataforma SIN que el pago venga
// atado a un ciclo (p. ej. tras pagar el único ciclo pendiente, envía un segundo pago suelto).
// ApprovePayment crea un ciclo nuevo para el tramo extendido; revertir debe BORRAR ese ciclo
// (no dejarlo huérfano en otro estado) y devolver la suscripción a su end_date anterior, para
// que el tenant pueda repetir el pago desde cero sin chocar con el índice único del ciclo.
func TestRevertApprovedPayment_noCycle_deletesCreatedCycleAndRestoresSubscription(t *testing.T) {
	db := setupApprovePaymentDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly}
	db.Create(&plan)
	tenant := database.Tenant{Name: "DORICONTA", Slug: "doriconta", DBName: "doriconta", Status: database.TenantStatusActive}
	db.Create(&tenant)

	// Anclado a hoy, no a una fecha fija: la escena es una renovación ANTICIPADA, así que la
	// suscripción tiene que estar vigente cuando corre el test. Con el 24/08/2026 escrito a mano
	// el test fue verde solo hasta que el calendario alcanzó esa fecha; a partir de ahí la
	// suscripción quedaba vencida y renewInPlaceTx arrancaba el tramo nuevo hoy —"no se regalan
	// días pasados", que es el comportamiento correcto—, así que fallaba la precondición.
	prevEnd := CalendarDateLima(NowLima()).AddDate(0, 0, 17)
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: database.SaasCycleMonthly,
		StartDate: prevEnd.AddDate(0, -1, 0), EndDate: EndOfDayLima(prevEnd), Status: database.SaasSubActive,
		BilledMonths: 1,
	}
	db.Create(&sub)

	// Sin ciclo pendiente vigente (ya lo pagó otro pago antes, como en el caso real) y sin
	// billing_cycle_id: cae en la rama "edge" de ApprovePayment.
	pay := database.SaasPayment{
		TenantID: tenant.ID, Amount: 99, Currency: "PEN", PeriodMonths: 1,
		Status: database.SaasPayPendingReview,
	}
	db.Create(&pay)

	if err := ApprovePayment(pay.ID, plan.ID, 1, "renovación anticipada", 1); err != nil {
		t.Fatalf("ApprovePayment: %v", err)
	}

	var extended database.SaasSubscription
	db.First(&extended, sub.ID)
	wantExtendedEnd := EndOfDayLima(prevEnd.AddDate(0, 1, 0))
	if !extended.EndDate.Equal(wantExtendedEnd) {
		t.Fatalf("EndDate tras aprobar = %s, want %s (encadenado desde el vencimiento anterior)",
			extended.EndDate.Format("2006-01-02"), wantExtendedEnd.Format("2006-01-02"))
	}

	var createdCycle database.SaasBillingCycle
	if err := db.Where("subscription_id = ? AND status = ?", sub.ID, database.SaasInvoicePaid).First(&createdCycle).Error; err != nil {
		t.Fatalf("no se encontró el ciclo creado y pagado por la aprobación: %v", err)
	}

	if err := RevertApprovedPayment(pay.ID, "prueba: pago duplicado de doriconta", 1); err != nil {
		t.Fatalf("RevertApprovedPayment: %v", err)
	}

	// El ciclo que la aprobación creó no debe quedar ni pending ni huérfano: se borra.
	var count int64
	db.Model(&database.SaasBillingCycle{}).Where("id = ?", createdCycle.ID).Count(&count)
	if count != 0 {
		t.Errorf("el ciclo creado por la aprobación revertida debía borrarse; sigue existiendo (id=%d)", createdCycle.ID)
	}

	var reloadedSub database.SaasSubscription
	db.First(&reloadedSub, sub.ID)
	if got, want := CalendarDateLima(reloadedSub.EndDate), prevEnd; !got.Equal(want) {
		t.Errorf("EndDate tras revertir = %s, want %s (el de antes de esta aprobación)", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}

	// El tenant debe poder repetir el pago: no debe quedar ningún ciclo pendiente/pagado colgado
	// del tramo revertido que choque con el índice único (subscription_id, period_end).
	var leftover int64
	db.Model(&database.SaasBillingCycle{}).Where("subscription_id = ?", sub.ID).Count(&leftover)
	if leftover != 0 {
		t.Errorf("quedaron %d ciclos para la suscripción tras revertir; se esperaba 0 (el original nunca existió, era el edge sin ciclo)", leftover)
	}
}

// No se puede revertir un pago si ya hay uno posterior aprobado sobre el mismo tenant: ese
// encadenó su período desde el estado que este dejó, y deshacer el más viejo primero rompería
// la cadena.
func TestRevertApprovedPayment_blocksWhenNewerApprovedPaymentExists(t *testing.T) {
	db := setupApprovePaymentDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly}
	db.Create(&plan)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	end := CalendarDateLima(time.Date(2026, 8, 24, 23, 59, 59, 0, lima()))
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: database.SaasCycleMonthly,
		StartDate: end.AddDate(0, -1, 0), EndDate: EndOfDayLima(end), Status: database.SaasSubActive,
	}
	db.Create(&sub)

	pay1 := database.SaasPayment{TenantID: tenant.ID, Amount: 99, Currency: "PEN", PeriodMonths: 1, Status: database.SaasPayPendingReview}
	db.Create(&pay1)
	if err := ApprovePayment(pay1.ID, plan.ID, 1, "primero", 1); err != nil {
		t.Fatalf("ApprovePayment #1: %v", err)
	}

	pay2 := database.SaasPayment{TenantID: tenant.ID, Amount: 99, Currency: "PEN", PeriodMonths: 1, Status: database.SaasPayPendingReview}
	db.Create(&pay2)
	if err := ApprovePayment(pay2.ID, plan.ID, 1, "segundo", 1); err != nil {
		t.Fatalf("ApprovePayment #2: %v", err)
	}

	if err := RevertApprovedPayment(pay1.ID, "intento de revertir el primero", 1); err == nil {
		t.Fatal("se esperaba error: hay un pago posterior (#2) ya aprobado")
	}
}
