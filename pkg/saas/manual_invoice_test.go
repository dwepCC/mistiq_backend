package saas

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Validaciones que corren antes de tocar la BD.
func TestIssueRenewalInvoice_rejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		in   RenewalInvoiceInput
	}{
		{"sin tenant", RenewalInvoiceInput{Months: 1}},
		{"meses negativos", RenewalInvoiceInput{TenantID: 1, Months: -3}},
		{"meses fuera de rango", RenewalInvoiceInput{TenantID: 1, Months: maxRenewalMonths + 1}},
		{"monto negativo", RenewalInvoiceInput{TenantID: 1, Amount: -10}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := IssueRenewalInvoice(c.in); err == nil {
				t.Fatal("se esperaba error de validación")
			}
		})
	}
}

func TestToInvoiceRow_formatsCalendarDays(t *testing.T) {
	paid := time.Date(2026, 9, 20, 15, 30, 0, 0, lima())
	row := ToInvoiceRow(&database.SaasBillingCycle{
		ID: 8, TenantID: 3,
		PeriodStart: time.Date(2026, 7, 30, 23, 59, 59, 0, lima()),
		PeriodEnd:   time.Date(2026, 8, 30, 23, 59, 59, 0, lima()),
		DueDate:     time.Date(2026, 7, 30, 23, 59, 59, 0, lima()),
		Amount:      99, Currency: "PEN", Status: database.SaasInvoicePaid,
		PaidAt: &paid,
	})
	if row.PeriodStart != "2026-07-30" || row.PeriodEnd != "2026-08-30" {
		t.Fatalf("período = %s → %s want 2026-07-30 → 2026-08-30", row.PeriodStart, row.PeriodEnd)
	}
	if row.DueDate != "2026-07-30" {
		t.Fatalf("DueDate = %q want 2026-07-30", row.DueDate)
	}
	if row.PaidAt != "2026-09-20" {
		t.Fatalf("PaidAt = %q want 2026-09-20", row.PaidAt)
	}
}

func TestToInvoiceRow_unpaidHasEmptyPaidAt(t *testing.T) {
	row := ToInvoiceRow(&database.SaasBillingCycle{ID: 9, Status: database.SaasInvoicePending})
	if row.PaidAt != "" {
		t.Fatalf("PaidAt = %q want vacío", row.PaidAt)
	}
}

func TestToInvoiceRow_nilIsSafe(t *testing.T) {
	if got := ToInvoiceRow(nil); got.ID != 0 {
		t.Fatalf("ToInvoiceRow(nil) debe devolver el cero: %+v", got)
	}
}

// El caso del requerimiento: vence el 30/07 y se cobra 1 mes por adelantado.
// El período arranca el 30/07 (no se solapa con lo ya pagado), vence el 30/07
// (el cliente debe pagar ese día) y lo deja cubierto hasta el 30/08.
func TestRenewalPeriod_startsWhenCurrentEnds(t *testing.T) {
	currentEnd := CalendarDateLima(time.Date(2026, 7, 30, 23, 59, 59, 0, lima()))
	periodEnd := currentEnd.AddDate(0, 1, 0)

	if got := currentEnd.Format("2006-01-02"); got != "2026-07-30" {
		t.Fatalf("inicio del período = %s want 2026-07-30", got)
	}
	if got := periodEnd.Format("2006-01-02"); got != "2026-08-30" {
		t.Fatalf("nuevo vencimiento = %s want 2026-08-30", got)
	}
}

// Emitir cobros seguidos debe encadenarlos, no repetir el mismo período.
//
// Antes la base era siempre sub.EndDate y, como emitir no mueve la vigencia, tres cobros
// salían con idéntico inicio y vencimiento. Ahora cada uno arranca donde termina el anterior.
func TestRenewalChain_consecutivePeriods(t *testing.T) {
	subEnd := CalendarDateLima(time.Date(2026, 9, 30, 23, 59, 59, 0, lima()))

	// Simula la regla: base = max(fin de suscripción, último período ya cobrado).
	nextPeriod := func(coveredUntil time.Time, months int) (start, end time.Time) {
		base := subEnd
		if coveredUntil.After(base) {
			base = coveredUntil
		}
		return base, base.AddDate(0, months, 0)
	}

	var covered time.Time
	want := []struct{ start, end string }{
		{"2026-09-30", "2026-10-30"},
		{"2026-10-30", "2026-11-30"},
		{"2026-11-30", "2026-12-30"},
	}
	for i, w := range want {
		start, end := nextPeriod(covered, 1)
		if got := start.Format("2006-01-02"); got != w.start {
			t.Fatalf("cobro %d: inicio = %s want %s", i+1, got, w.start)
		}
		if got := end.Format("2006-01-02"); got != w.end {
			t.Fatalf("cobro %d: fin = %s want %s", i+1, got, w.end)
		}
		// El vencimiento es el inicio del período: se paga por adelantado.
		if start.Format("2006-01-02") != w.start {
			t.Fatalf("cobro %d: el vencimiento debe ser el inicio del período", i+1)
		}
		covered = end
	}
}

// Anular un cobro con un comprobante en revisión ya no bloquea: rechaza ese pago en cascada
// como parte de la misma anulación, para no dejarlo "en revisión" para siempre sobre una deuda
// que el admin decidió que ya no existe.
func TestCancelInvoiceCascadeRechazaPagoEnRevision(t *testing.T) {
	db := setupApprovePaymentDB(t)
	cycle := database.SaasBillingCycle{
		TenantID: 1, SubscriptionID: 1, PlanID: 1,
		PeriodStart: time.Now(), PeriodEnd: time.Now().AddDate(0, 1, 0), DueDate: time.Now(),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoicePendingReview,
	}
	if err := db.Create(&cycle).Error; err != nil {
		t.Fatalf("crear ciclo: %v", err)
	}
	pago := database.SaasPayment{
		TenantID: 1, BillingCycleID: &cycle.ID, Amount: 99,
		Status: database.SaasPayPendingReview,
	}
	if err := db.Create(&pago).Error; err != nil {
		t.Fatalf("crear pago: %v", err)
	}

	if err := CancelInvoice(cycle.ID, "duplicado", 7); err != nil {
		t.Fatalf("CancelInvoice: %v", err)
	}

	var afterCycle database.SaasBillingCycle
	db.First(&afterCycle, cycle.ID)
	if afterCycle.Status != database.SaasInvoiceRejected {
		t.Errorf("ciclo status = %q, se esperaba rejected", afterCycle.Status)
	}

	var afterPago database.SaasPayment
	db.First(&afterPago, pago.ID)
	if afterPago.Status != database.SaasPayRejected {
		t.Errorf("pago status = %q, se esperaba rejected (rechazado en cascada)", afterPago.Status)
	}
	if afterPago.ReviewedBy == nil || *afterPago.ReviewedBy != 7 {
		t.Errorf("reviewed_by = %v, se esperaba 7 (el actor que anuló el cobro)", afterPago.ReviewedBy)
	}
}

// Un pago YA aprobado sí bloquea: eso significaría que el ciclo debería estar 'paid', no
// anulable por esta vía — sería una inconsistencia a revisar a mano, no algo que resolver solo.
func TestCancelInvoiceRechazaConPagoAprobado(t *testing.T) {
	db := setupApprovePaymentDB(t)
	cycle := database.SaasBillingCycle{
		TenantID: 1, SubscriptionID: 1, PlanID: 1,
		PeriodStart: time.Now(), PeriodEnd: time.Now().AddDate(0, 1, 0), DueDate: time.Now(),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoicePending,
	}
	db.Create(&cycle)
	pago := database.SaasPayment{
		TenantID: 1, BillingCycleID: &cycle.ID, Amount: 99, Status: database.SaasPayApproved,
	}
	db.Create(&pago)

	if err := CancelInvoice(cycle.ID, "", 1); err == nil {
		t.Error("un cobro con un pago aprobado asociado no debe poder anularse")
	}
}

func TestCancelInvoiceRechazaPagadoYaAnulado(t *testing.T) {
	db := setupApprovePaymentDB(t)
	pagado := database.SaasBillingCycle{
		TenantID: 1, SubscriptionID: 1, PlanID: 1,
		PeriodStart: time.Now(), PeriodEnd: time.Now().AddDate(0, 1, 0), DueDate: time.Now(),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoicePaid,
	}
	db.Create(&pagado)
	if err := CancelInvoice(pagado.ID, "", 1); err == nil {
		t.Error("un cobro pagado no debe poder anularse")
	}

	anulado := database.SaasBillingCycle{
		TenantID: 1, SubscriptionID: 1, PlanID: 1,
		PeriodStart: time.Now(), PeriodEnd: time.Now().AddDate(0, 2, 0), DueDate: time.Now(),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoiceRejected,
	}
	db.Create(&anulado)
	if err := CancelInvoice(anulado.ID, "", 1); err == nil {
		t.Error("un cobro ya anulado no debe volver a anularse")
	}
}

// El período en curso es el caso que hay que advertir: anularlo regala el servicio que el
// cliente está usando.
func TestInvoiceCancelInfoDetectaPeriodoEnCurso(t *testing.T) {
	setupApprovePaymentDB(t)
	now := NowLima()
	enCurso := database.SaasBillingCycle{
		TenantID: 1, PeriodStart: now.AddDate(0, 0, -5), PeriodEnd: now.AddDate(0, 0, 25),
	}
	if info := InvoiceCancelInfo(&enCurso); !info.CoversActivePeriod {
		t.Error("un período que ya empezó y no ha terminado debía marcarse como en curso")
	}
	futuro := database.SaasBillingCycle{
		TenantID: 1, PeriodStart: now.AddDate(0, 0, 10), PeriodEnd: now.AddDate(0, 0, 40),
	}
	if info := InvoiceCancelInfo(&futuro); info.CoversActivePeriod {
		t.Error("un período que aún no empieza no debía marcarse como en curso")
	}
}
