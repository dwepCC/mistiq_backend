package saas

import (
	"testing"

	"tukifac/pkg/database"
)

// SyncPlanCycles siempre deja exactamente los 4 ciclos fijos, nunca más ni menos, incluso si el
// input manda un months fuera del set (se ignora) o viene vacío (todos quedan sin descuento).
func TestSyncPlanCycles_alwaysExactlyFourFixedCycles(t *testing.T) {
	db := setupApprovePaymentDB(t)
	plan := database.SaasPlan{Name: "Pro", Price: 20, Active: true}
	db.Create(&plan)

	SyncPlanCycles(plan.ID, []PlanCycleInput{
		{Months: 3, DiscountType: DiscountFixed, DiscountValue: 10, Enabled: true}, // 3 meses: 60-10=50
		{Months: 6, DiscountType: DiscountPercent, DiscountValue: 50, Enabled: true}, // 6 meses: 120*0.5=60
		{Months: 99, DiscountType: DiscountFixed, DiscountValue: 999, Enabled: true}, // fuera del set fijo: se ignora
	})

	rows := LoadPlanCycles(plan.ID)
	if len(rows) != 4 {
		t.Fatalf("esperaba exactamente 4 ciclos, got %d", len(rows))
	}
	byMonths := map[int]database.SaasPlanCycle{}
	for _, r := range rows {
		byMonths[r.Months] = r
	}
	for _, m := range []int{1, 3, 6, 12} {
		if _, ok := byMonths[m]; !ok {
			t.Errorf("falta el ciclo de %d meses", m)
		}
	}
	if byMonths[1].DiscountType != "" {
		t.Errorf("1 mes debería quedar sin descuento (no vino en el input), got %+v", byMonths[1])
	}
	if byMonths[3].DiscountType != DiscountFixed || byMonths[3].DiscountValue != 10 {
		t.Errorf("3 meses = %+v, want fixed 10", byMonths[3])
	}
}

// El bug real que se evita: el ejemplo del usuario era literal — plan de 20/mes, 3 meses debía
// pagar 50 (no 60), calculado a través de ComputeCycleAmounts vía BuildPlanCycleViews.
func TestBuildPlanCycleViews_computesDiscountedTotals(t *testing.T) {
	db := setupApprovePaymentDB(t)
	plan := database.SaasPlan{Name: "Pro", Price: 20, Active: true}
	db.Create(&plan)
	SyncPlanCycles(plan.ID, []PlanCycleInput{
		{Months: 3, DiscountType: DiscountFixed, DiscountValue: 10, Enabled: true},
	})

	views := BuildPlanCycleViews(plan, LoadPlanCycles(plan.ID))
	var threeMonths *PlanCycleView
	for i := range views {
		if views[i].Months == 3 {
			threeMonths = &views[i]
		}
	}
	if threeMonths == nil {
		t.Fatal("no se encontró el ciclo de 3 meses")
	}
	if threeMonths.GrossAmount != 60 {
		t.Errorf("bruto = %v, want 60 (20×3)", threeMonths.GrossAmount)
	}
	if threeMonths.NetAmount != 50 {
		t.Errorf("neto = %v, want 50 (60-10), este es exactamente el ejemplo del requerimiento", threeMonths.NetAmount)
	}
}

// FindEnabledPlanCycle no debe devolver un ciclo deshabilitado — el tenant no puede elegir algo
// que el admin ocultó, aunque tenga un descuento configurado.
func TestFindEnabledPlanCycle_ignoresDisabled(t *testing.T) {
	db := setupApprovePaymentDB(t)
	plan := database.SaasPlan{Name: "Pro", Price: 20, Active: true}
	db.Create(&plan)
	SyncPlanCycles(plan.ID, []PlanCycleInput{
		{Months: 6, DiscountType: DiscountPercent, DiscountValue: 20, Enabled: false},
	})

	views := BuildPlanCycleViews(plan, LoadPlanCycles(plan.ID))
	if c := FindEnabledPlanCycle(views, 6); c != nil {
		t.Errorf("el ciclo de 6 meses está deshabilitado, no debería poder elegirse: %+v", c)
	}
	if c := FindEnabledPlanCycle(views, 1); c == nil {
		t.Error("el ciclo de 1 mes sigue habilitado por defecto, debería encontrarse")
	}
}
