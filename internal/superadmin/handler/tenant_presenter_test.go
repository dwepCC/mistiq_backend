package handler

import (
	"testing"

	"tukifac/internal/superadmin/service"
	"tukifac/pkg/database"
)

// El panel central edita estos campos: si el presenter deja de devolver alguno, el
// formulario lo reabre con el valor por defecto y parece que la edición no se guardó.
// Eso fue exactamente lo que pasó con taxpayer_regime.
func TestEnrichTenantMap_exposesEditableFields(t *testing.T) {
	tenant := &database.Tenant{
		Name:           "Demo SAC",
		Slug:           "demo",
		Plan:           "Basic",
		Status:         "active",
		Email:          "demo@demo.com",
		Phone:          "999888777",
		RUC:            "20123456789",
		Rubro:          "general",
		TaxpayerRegime: "nrus",
		Address:        "Av. Demo 123",
		Ubigeo:         "150101",
	}

	m := enrichTenantMap(tenant)

	for field, want := range map[string]string{
		"name":            "Demo SAC",
		"plan":            "Basic",
		"status":          "active",
		"email":           "demo@demo.com",
		"phone":           "999888777",
		"ruc":             "20123456789",
		"rubro":           "general",
		"taxpayer_regime": "nrus",
		"address":         "Av. Demo 123",
		"ubigeo":          "150101",
	} {
		got, ok := m[field]
		if !ok {
			t.Errorf("el presenter no devuelve %q (el formulario lo necesita para preseleccionar)", field)
			continue
		}
		if got != want {
			t.Errorf("%s = %v want %v", field, got, want)
		}
	}
}

func TestWithPlanRef_addsPlanIdentity(t *testing.T) {
	m := withPlanRef(enrichTenantMap(&database.Tenant{Plan: "Basic"}), service.TenantPlanRef{
		PlanID:   4,
		PlanName: "Basic",
	})
	if m["plan_id"] != uint(4) {
		t.Fatalf("plan_id = %v want 4", m["plan_id"])
	}
	if m["plan_name"] != "Basic" {
		t.Fatalf("plan_name = %v want Basic", m["plan_name"])
	}
}

// Plan huérfano: plan_id 0 y sin plan_name, para que el panel pida elegir uno válido.
func TestWithPlanRef_unknownPlanLeavesNameEmpty(t *testing.T) {
	m := withPlanRef(enrichTenantMap(&database.Tenant{Plan: "plan-viejo"}), service.TenantPlanRef{})
	if m["plan_id"] != uint(0) {
		t.Fatalf("plan_id = %v want 0", m["plan_id"])
	}
	if _, ok := m["plan_name"]; ok {
		t.Fatal("plan_name no debe estar presente si el plan no existe en el catálogo")
	}
}
