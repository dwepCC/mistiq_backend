package saas

import (
	"os"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupApprovePaymentDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:approve_pay_test.db?_journal_mode=WAL&_busy_timeout=15000"
	t.Cleanup(func() { os.Remove("approve_pay_test.db") })
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	database.CentralDB = db
	if err := db.AutoMigrate(
		&database.Tenant{},
		&database.SaasPlan{},
		&database.SaasPlanModule{},
		&database.TenantModule{},
		&database.SaasSubscription{},
		&database.SaasBillingCycle{},
		&database.SaasPayment{},
		&database.SaasPlatformSettings{},
		&database.SaasSubscriptionEvent{},
		&database.SaasNotificationLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, tbl := range []string{
		"saas_payments", "saas_billing_cycles", "saas_subscriptions",
		"saas_plans", "tenants", "saas_subscription_events", "tenant_modules",
	} {
		db.Exec("DELETE FROM " + tbl)
	}
	return db
}

// El bug que corregimos: pagar una deuda de 6 meses debe extender la suscripción 6 meses
// (hasta el period_end del ciclo pagado), NO 1 mes del plan. Y no debe quedar deuda fantasma.
func TestApprovePayment_extendsToPaidCyclePeriod(t *testing.T) {
	db := setupApprovePaymentDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly}
	db.Create(&plan)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	curEnd := CalendarDateLima(time.Date(2026, 1, 31, 12, 0, 0, 0, lima()))
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: database.SaasCycleMonthly,
		StartDate: curEnd.AddDate(0, -1, 0), EndDate: EndOfDayLima(curEnd), Status: database.SaasSubActive,
	}
	db.Create(&sub)

	// Deuda de renovación de 6 meses (feb→jul).
	periodStart := curEnd
	periodEnd := curEnd.AddDate(0, 6, 0)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: sub.ID, PlanID: plan.ID,
		PeriodStart: EndOfDayLima(periodStart), PeriodEnd: EndOfDayLima(periodEnd),
		DueDate: EndOfDayLima(periodStart), Amount: 594, Currency: "PEN",
		Status: database.SaasInvoicePending,
	}
	db.Create(&cycle)

	pay := database.SaasPayment{
		TenantID: tenant.ID, BillingCycleID: &cycle.ID, Amount: 594, Currency: "PEN",
		Status: database.SaasPayPendingReview,
	}
	db.Create(&pay)

	if err := ApprovePayment(pay.ID, 0, 0, "ok", 1); err != nil {
		t.Fatalf("ApprovePayment: %v", err)
	}

	// La suscripción vigente debe terminar en el period_end pagado (jul), no feb.
	var active database.SaasSubscription
	if err := db.Where("tenant_id = ? AND status = ?", tenant.ID, database.SaasSubActive).
		Order("created_at desc").First(&active).Error; err != nil {
		t.Fatalf("sin suscripción activa: %v", err)
	}
	if got, want := CalendarDateLima(active.EndDate), CalendarDateLima(periodEnd); !got.Equal(want) {
		t.Errorf("EndDate = %s, want %s (period_end del ciclo pagado)", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}

	// El ciclo pagado queda 'paid'.
	var c database.SaasBillingCycle
	db.First(&c, cycle.ID)
	if c.Status != database.SaasInvoicePaid {
		t.Errorf("ciclo status = %q, want paid", c.Status)
	}

	// No debe quedar ninguna deuda pendiente (nada de deuda fantasma del período recién pagado).
	var pending int64
	db.Model(&database.SaasBillingCycle{}).Where("tenant_id = ? AND status = ?", tenant.ID, database.SaasInvoicePending).Count(&pending)
	if pending != 0 {
		t.Errorf("quedaron %d ciclos pendientes tras pagar; se esperaba 0", pending)
	}
}

// Un pago que no cubre la deuda debe rechazar la aprobación (exigir pago completo).
func TestApprovePayment_rejectsPartialPayment(t *testing.T) {
	db := setupApprovePaymentDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 300, BillingCycle: database.SaasCycleMonthly}
	db.Create(&plan)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	end := CalendarDateLima(time.Date(2026, 2, 28, 12, 0, 0, 0, lima()))
	sub := database.SaasSubscription{TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: database.SaasCycleMonthly, StartDate: end.AddDate(0, -1, 0), EndDate: EndOfDayLima(end), Status: database.SaasSubActive}
	db.Create(&sub)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: sub.ID, PlanID: plan.ID,
		PeriodStart: EndOfDayLima(end), PeriodEnd: EndOfDayLima(end.AddDate(0, 1, 0)),
		DueDate: EndOfDayLima(end), Amount: 300, Currency: "PEN", Status: database.SaasInvoicePending,
	}
	db.Create(&cycle)
	pay := database.SaasPayment{TenantID: tenant.ID, BillingCycleID: &cycle.ID, Amount: 50, Currency: "PEN", Status: database.SaasPayPendingReview}
	db.Create(&pay)

	if err := ApprovePayment(pay.ID, 0, 0, "parcial", 1); err == nil {
		t.Fatal("se esperaba rechazo por pago insuficiente")
	}
	var c database.SaasBillingCycle
	db.First(&c, cycle.ID)
	if c.Status != database.SaasInvoicePending {
		t.Errorf("el ciclo no debe marcarse pagado con pago parcial (status=%q)", c.Status)
	}
	var p database.SaasPayment
	db.First(&p, pay.ID)
	if p.Status == database.SaasPayApproved {
		t.Error("el pago parcial no debe quedar aprobado")
	}
}
