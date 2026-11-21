package saas

import (
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/saas/docusage"

	"gorm.io/gorm"
)

// El descuento del cobro inicial de una empresa nueva ya no se recibe del formulario: sale del
// ciclo configurado en el plan para esa cantidad de meses (mismo criterio que el autoservicio
// del tenant) — sin importar qué se le pase a ProvisionInitialSubscription, no toma nada más.
func TestProvisionInitialSubscription_usesPlanCycleDiscount(t *testing.T) {
	db := setupApprovePaymentDB(t)
	plan := database.SaasPlan{Name: "Pro", Price: 100, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)
	// 3 meses con 15% de descuento configurado en el plan.
	db.Create(&database.SaasPlanCycle{PlanID: plan.ID, Months: 3, DiscountType: "percent", DiscountValue: 15, Enabled: true})
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	sub, err := ProvisionInitialSubscription(tenant.ID, "Pro", 3, "alta", nil)
	if err != nil {
		t.Fatalf("ProvisionInitialSubscription: %v", err)
	}

	var cycle database.SaasBillingCycle
	if err := db.Where("subscription_id = ?", sub.ID).First(&cycle).Error; err != nil {
		t.Fatalf("ciclo inicial no encontrado: %v", err)
	}
	// Precio 100 × 3 meses = 300 bruto; 15% de descuento = 45 → neto 255.
	if cycle.GrossAmount != 300 {
		t.Errorf("gross_amount = %.2f, se esperaba 300.00", cycle.GrossAmount)
	}
	if cycle.DiscountType != "percent" || cycle.DiscountValue != 15 {
		t.Errorf("descuento = %s %.2f, se esperaba percent 15.00 (el del plan, no uno manual)", cycle.DiscountType, cycle.DiscountValue)
	}
	if cycle.Amount != 255 {
		t.Errorf("amount = %.2f, se esperaba 255.00 (300 - 15%%)", cycle.Amount)
	}
}

// Si el plan no tiene un ciclo configurado (ni habilitado) para esa cantidad de meses, el cobro
// sale sin descuento — precio pleno, no un error ni un descuento heredado de otro ciclo.
func TestProvisionInitialSubscription_noDiscountWhenMonthsNotConfigured(t *testing.T) {
	db := setupApprovePaymentDB(t)
	plan := database.SaasPlan{Name: "Pro", Price: 100, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)
	// Solo el ciclo de 6 meses tiene descuento — 4 meses (lo que se va a pedir) no calza con nada.
	db.Create(&database.SaasPlanCycle{PlanID: plan.ID, Months: 6, DiscountType: "percent", DiscountValue: 20, Enabled: true})
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	sub, err := ProvisionInitialSubscription(tenant.ID, "Pro", 4, "alta", nil)
	if err != nil {
		t.Fatalf("ProvisionInitialSubscription: %v", err)
	}

	var cycle database.SaasBillingCycle
	db.Where("subscription_id = ?", sub.ID).First(&cycle)
	if cycle.DiscountValue != 0 {
		t.Errorf("discount_value = %.2f, se esperaba 0 (4 meses no tiene ciclo configurado)", cycle.DiscountValue)
	}
	if cycle.Amount != 400 {
		t.Errorf("amount = %.2f, se esperaba 400.00 (100 × 4, sin descuento)", cycle.Amount)
	}
}

func bonusTestPlan(t *testing.T, db interface {
	Create(value interface{}) *gorm.DB
}, discountPct float64) database.SaasPlan {
	t.Helper()
	plan := database.SaasPlan{Name: "Pro", Price: 100, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)
	db.Create(&database.SaasPlanCycle{PlanID: plan.ID, Months: 12, DiscountType: "percent", DiscountValue: discountPct, Enabled: true})
	return plan
}

// 12 meses + 2 de cortesía: la vigencia dura 14 meses pero el cobro sigue siendo el de 12 (con el
// descuento del ciclo de 12 meses), billed_months=12 y el bonus queda registrado.
func TestProvisionInitialSubscriptionWithBonus_vigenciaMasLargaMismoCobro(t *testing.T) {
	db := setupApprovePaymentDB(t)
	bonusTestPlan(t, db, 10)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	start := CalendarDateLima(NowLima()).AddDate(0, 0, 3)
	sub, err := ProvisionInitialSubscriptionWithBonus(tenant.ID, "Pro", 12, 2, "alta", &start)
	if err != nil {
		t.Fatalf("ProvisionInitialSubscriptionWithBonus: %v", err)
	}
	if got, want := CalendarDateLima(sub.EndDate), start.AddDate(0, 14, 0); !got.Equal(want) {
		t.Errorf("EndDate = %s, want %s (12 + 2 meses)", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
	if sub.BilledMonths != 12 || sub.BonusMonths != 2 {
		t.Errorf("billed/bonus = %d/%d, want 12/2", sub.BilledMonths, sub.BonusMonths)
	}

	var cycle database.SaasBillingCycle
	if err := db.Where("subscription_id = ?", sub.ID).First(&cycle).Error; err != nil {
		t.Fatal(err)
	}
	// 100 × 12 = 1200 bruto − 10% = 1080; los 2 meses de regalo no suman al cobro.
	if cycle.GrossAmount != 1200 || cycle.Amount != 1080 || cycle.MonthsCovered != 12 || cycle.BonusMonths != 2 {
		t.Errorf("ciclo gross=%.2f amount=%.2f months=%d bonus=%d, want 1200/1080/12/2",
			cycle.GrossAmount, cycle.Amount, cycle.MonthsCovered, cycle.BonusMonths)
	}
	if got, want := CalendarDateLima(cycle.PeriodEnd), start.AddDate(0, 14, 0); !got.Equal(want) {
		t.Errorf("PeriodEnd del ciclo = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
	// Los meses gratis también tienen cupo mensual de comprobantes: 14 períodos mensuales.
	if n := docusage.TotalCycleQuotaPeriods(&cycle); n != 14 {
		t.Errorf("períodos de cuota = %d, want 14", n)
	}
}

// Alta con bonus y pago inmediato contra su ciclo: el pago (monto de 12 meses) deja el ciclo pagado
// y la vigencia sigue en 14 meses, con billed_months=12.
func TestProvisionInitialSubscriptionWithBonus_pagoInmediatoConservaVigencia(t *testing.T) {
	db := setupApprovePaymentDB(t)
	bonusTestPlan(t, db, 0)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	sub, err := ProvisionInitialSubscriptionWithBonus(tenant.ID, "Pro", 12, 2, "alta", nil)
	if err != nil {
		t.Fatal(err)
	}
	var cycle database.SaasBillingCycle
	db.Where("subscription_id = ?", sub.ID).First(&cycle)
	wantEnd := CalendarDateLima(sub.EndDate)

	pay := database.SaasPayment{TenantID: tenant.ID, BillingCycleID: &cycle.ID, Amount: cycle.Amount, PeriodMonths: 12,
		Currency: "PEN", Status: database.SaasPayPendingReview}
	db.Create(&pay)
	if err := ApprovePayment(pay.ID, 0, 0, "ok", 1, nil); err != nil {
		t.Fatalf("ApprovePayment: %v", err)
	}

	var after database.SaasSubscription
	db.First(&after, sub.ID)
	if got := CalendarDateLima(after.EndDate); !got.Equal(wantEnd) {
		t.Errorf("EndDate tras pagar = %s, want %s (la vigencia con bonus no se acorta)", got.Format("2006-01-02"), wantEnd.Format("2006-01-02"))
	}
	if after.BilledMonths != 12 {
		t.Errorf("billed_months = %d, want 12", after.BilledMonths)
	}
	var c database.SaasBillingCycle
	db.First(&c, cycle.ID)
	if c.Status != database.SaasInvoicePaid {
		t.Errorf("ciclo status = %q, want paid", c.Status)
	}
}

func TestValidateBonusMonths(t *testing.T) {
	cases := []struct {
		months, bonus int
		ok            bool
	}{
		{12, 0, true}, {12, 2, true}, {12, 6, true}, {12, 7, false}, {12, -1, false},
		{6, 2, false}, {1, 1, false}, {3, 0, true}, {6, 0, true},
	}
	for _, c := range cases {
		if err := ValidateBonusMonths(c.months, c.bonus); (err == nil) != c.ok {
			t.Errorf("ValidateBonusMonths(%d,%d) err=%v, ok esperado=%v", c.months, c.bonus, err, c.ok)
		}
	}
	db := setupApprovePaymentDB(t)
	bonusTestPlan(t, db, 0)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	if _, err := ProvisionInitialSubscriptionWithBonus(tenant.ID, "Pro", 6, 2, "x", nil); err == nil {
		t.Error("el bonus con 6 meses debe rechazarse")
	}
	var n int64
	db.Model(&database.SaasSubscription{}).Where("tenant_id = ?", tenant.ID).Count(&n)
	if n != 0 {
		t.Errorf("un alta rechazada no debe dejar suscripciones (hay %d)", n)
	}
}
