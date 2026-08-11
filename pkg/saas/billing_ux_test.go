package saas

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
)

func TestShouldShowRenewalBanner(t *testing.T) {
	days := []int{7, 5, 3, 1}
	if ShouldShowRenewalBanner(365, database.SaasSubActive, days) {
		t.Fatal("365 days should not show renewal")
	}
	if !ShouldShowRenewalBanner(5, database.SaasSubActive, days) {
		t.Fatal("5 days should show renewal")
	}
	if ShouldShowRenewalBanner(0, database.SaasSubActive, days) {
		t.Fatal("expired should not show renewal banner")
	}
}

func TestBillingDebtApplies_farDueDate(t *testing.T) {
	cfg := PlatformSettings{ReminderDays: []int{7, 5, 3, 1}}
	sub := TenantSubscriptionView{
		Status:          database.SaasSubActive,
		DaysUntilExpiry: 365,
		PendingAmount:   99,
	}
	due := time.Now().In(lima()).AddDate(1, 0, 0)
	cycle := &database.SaasBillingCycle{
		Status:  database.SaasInvoicePending,
		DueDate: due,
		Amount:  99,
	}
	if billingDebtApplies(cycle, sub, cfg) {
		t.Fatal("pending invoice far in future is not real debt")
	}
}

func TestBillingDebtApplies_withinReminder(t *testing.T) {
	cfg := PlatformSettings{ReminderDays: []int{7, 5, 3, 1}}
	sub := TenantSubscriptionView{Status: database.SaasSubActive, DaysUntilExpiry: 5}
	due := time.Now().In(lima()).AddDate(0, 0, 5)
	cycle := &database.SaasBillingCycle{Status: database.SaasInvoicePending, DueDate: due}
	if !billingDebtApplies(cycle, sub, cfg) {
		t.Fatal("due within reminder window is real debt")
	}
}

func TestMaxReminderDay(t *testing.T) {
	if MaxReminderDay([]int{7, 3, 12}) != 12 {
		t.Fatalf("expected 12 got %d", MaxReminderDay([]int{7, 3, 12}))
	}
}

// Vencido y suspendido cortan igual el acceso, pero el tenant ve las dos cosas a la vez: el
// widget muestra el estado y el banner el mensaje. La rama de suspensión capturaba también al
// vencido —porque tampoco puede operar—, así que se leía «VENCIDA» junto a «Tu cuenta está
// suspendida».
func TestResolvePaymentUXDistingueVencidoDeSuspendido(t *testing.T) {
	cfg := PlatformSettings{ReminderDays: []int{7, 5, 3, 1}}

	vencido := TenantSubscriptionView{
		Status:              database.SaasSubOverdue,
		IsOverdue:           true,
		CanOperate:          false,
		ShowSuspendedBanner: true,
	}
	if tier, _, _ := resolvePaymentUX(vencido, cfg, true, 99); tier != "overdue" {
		t.Errorf("suscripción vencida: tier = %q, se esperaba overdue", tier)
	}

	suspendido := TenantSubscriptionView{
		Status:              database.SaasSubSuspended,
		IsSuspended:         true,
		CanOperate:          false,
		ShowSuspendedBanner: true,
	}
	if tier, _, _ := resolvePaymentUX(suspendido, cfg, true, 99); tier != "suspended" {
		t.Errorf("suscripción suspendida: tier = %q, se esperaba suspended", tier)
	}
}

// El mensaje de vencido debe decir que el acceso está cortado: quedarse en «regulariza tu
// pago» hacía creer que era solo un recordatorio.
func TestStatusBannerVencidoAvisaDelCorte(t *testing.T) {
	sub := TenantSubscriptionView{Status: database.SaasSubOverdue, IsOverdue: true}
	ctx := BillingContextView{UrgencyTier: "overdue", HasRealDebt: true, DisplayDebtAmount: 99}
	show, variant, msg := resolveStatusBanner(sub, ctx)
	if !show || variant != "danger" {
		t.Errorf("banner de vencido: show=%v variant=%q, se esperaba visible y danger", show, variant)
	}
	if !strings.Contains(msg, "restringido") || !strings.Contains(msg, "99.00") {
		t.Errorf("mensaje = %q; debía avisar del corte de acceso y del monto", msg)
	}
}

// Un alta reciente tiene el cobro pendiente desde el día uno (prepago) pero el plan lejos de
// vencer. Ese caso caía en «reminder» y el aviso decía «tu plan vence en 31 días» justo
// después de registrar al cliente: lo pendiente es el pago, no la renovación.
func TestPagoPendienteNoSeAnunciaComoVencimientoDePlan(t *testing.T) {
	cfg := PlatformSettings{ReminderDays: []int{7, 5, 3, 1}, PaymentWindowDays: 3}
	sub := TenantSubscriptionView{
		Status:          database.SaasSubActive,
		DaysUntilExpiry: 31, // el plan recién empieza
		CanOperate:      true,
	}

	tier, label, _ := resolvePaymentUX(sub, cfg, true, 99)
	if tier != "payment_due" {
		t.Errorf("tier = %q, se esperaba payment_due", tier)
	}
	if label != "Pendiente S/ 99.00" {
		t.Errorf("label = %q", label)
	}

	ctx := BillingContextView{UrgencyTier: tier, HasRealDebt: true, DisplayDebtAmount: 99, PaymentDaysLeft: 3}
	_, _, msg := resolveStatusBanner(sub, ctx)
	if strings.Contains(msg, "vence en 31") || strings.Contains(msg, "renovación") {
		t.Errorf("el aviso habla del vencimiento del plan: %q", msg)
	}
	if !strings.Contains(msg, "3 día(s)") || !strings.Contains(msg, "99.00") {
		t.Errorf("el aviso debía indicar el plazo de pago y el monto: %q", msg)
	}

	// Con el plan sí por vencer, manda el recordatorio de renovación.
	porVencer := TenantSubscriptionView{Status: database.SaasSubActive, DaysUntilExpiry: 3, CanOperate: true}
	if tier, _, _ := resolvePaymentUX(porVencer, cfg, true, 99); tier != "reminder" {
		t.Errorf("plan por vencer: tier = %q, se esperaba reminder", tier)
	}
}

// Agotado el plazo, el mismo estado sube de tono y avisa de la suspensión.
func TestPagoPendienteConPlazoAgotado(t *testing.T) {
	sub := TenantSubscriptionView{Status: database.SaasSubActive, DaysUntilExpiry: 25}
	ctx := BillingContextView{UrgencyTier: "payment_due", HasRealDebt: true, DisplayDebtAmount: 99, PaymentDaysLeft: -2}
	_, variant, msg := resolveStatusBanner(sub, ctx)
	if variant != "danger" {
		t.Errorf("variant = %q, se esperaba danger", variant)
	}
	if !strings.Contains(msg, "venció") || !strings.Contains(msg, "suspensión") {
		t.Errorf("mensaje = %q; debía avisar del plazo vencido y del riesgo de suspensión", msg)
	}
}
