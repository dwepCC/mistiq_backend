package saas

import (
	"testing"

	"tukifac/pkg/database"
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
