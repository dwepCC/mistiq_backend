package saas

import "tukifac/pkg/database"

// PlanCycleInput lo que el admin configura para un ciclo fijo al crear/editar un plan.
type PlanCycleInput struct {
	Months        int     `json:"months"`
	DiscountType  string  `json:"discount_type"`
	DiscountValue float64 `json:"discount_value"`
	Enabled       bool    `json:"enabled"`
}

// PlanCycleView un ciclo fijo con su precio ya calculado — lo que consumen tanto el panel
// central (para mostrar/editar) como el selector del tenant (para elegir y pagar), así ninguno
// de los dos vuelve a calcular precio × meses ni el descuento por su cuenta.
type PlanCycleView struct {
	Months        int     `json:"months"`
	DiscountType  string  `json:"discount_type"`
	DiscountValue float64 `json:"discount_value"`
	Enabled       bool    `json:"enabled"`
	GrossAmount   float64 `json:"gross_amount"`
	NetAmount     float64 `json:"net_amount"`
}

// SyncPlanCycles reemplaza los ciclos de un plan por exactamente los 4 fijos (FixedPlanCycleMonths),
// tomando el descuento de `inputs` cuando venga uno para ese months, o "sin descuento, habilitado"
// si no vino nada — nunca deja un plan con menos o más de 4 filas, ni con un months fuera del
// set fijo (un months inválido en el input simplemente se ignora).
func SyncPlanCycles(planID uint, inputs []PlanCycleInput) {
	byMonths := make(map[int]PlanCycleInput, len(inputs))
	for _, in := range inputs {
		if IsFixedPlanCycleMonths(in.Months) {
			byMonths[in.Months] = in
		}
	}
	database.CentralDB.Where("plan_id = ?", planID).Delete(&database.SaasPlanCycle{})
	for _, months := range FixedPlanCycleMonths {
		row := database.SaasPlanCycle{PlanID: planID, Months: months, Enabled: true}
		if in, ok := byMonths[months]; ok {
			norm, err := NormalizeDiscount(Discount{Type: in.DiscountType, Value: in.DiscountValue})
			if err == nil {
				row.DiscountType = norm.Type
				row.DiscountValue = norm.Value
			}
			row.Enabled = in.Enabled
		}
		database.CentralDB.Create(&row)
	}
}

// LoadPlanCycles los 4 ciclos fijos de un plan, completando con "sin descuento, habilitado"
// cualquiera que todavía no tenga fila (plan creado antes de este feature, o recién creado antes
// de que se llame SyncPlanCycles la primera vez) — nunca devuelve menos de 4.
func LoadPlanCycles(planID uint) []database.SaasPlanCycle {
	var rows []database.SaasPlanCycle
	database.CentralDB.Where("plan_id = ?", planID).Find(&rows)
	byMonths := make(map[int]database.SaasPlanCycle, len(rows))
	for _, r := range rows {
		byMonths[r.Months] = r
	}
	out := make([]database.SaasPlanCycle, 0, len(FixedPlanCycleMonths))
	for _, months := range FixedPlanCycleMonths {
		if r, ok := byMonths[months]; ok {
			out = append(out, r)
		} else {
			out = append(out, database.SaasPlanCycle{PlanID: planID, Months: months, Enabled: true})
		}
	}
	return out
}

// BuildPlanCycleViews calcula bruto/neto para cada ciclo del plan — una sola vez acá, para que
// panel central y tenant siempre muestren y cobren exactamente lo mismo.
func BuildPlanCycleViews(plan database.SaasPlan, rows []database.SaasPlanCycle) []PlanCycleView {
	out := make([]PlanCycleView, 0, len(rows))
	for _, r := range rows {
		amounts := ComputeCycleAmounts(plan.Price, r.Months, Discount{Type: r.DiscountType, Value: r.DiscountValue})
		out = append(out, PlanCycleView{
			Months: r.Months, DiscountType: amounts.Discount.Type, DiscountValue: amounts.Discount.Value,
			Enabled: r.Enabled, GrossAmount: amounts.Gross, NetAmount: amounts.Net,
		})
	}
	return out
}

// FindEnabledPlanCycle busca el ciclo habilitado de `months` entre las vistas ya calculadas de
// un plan. nil si months no es un ciclo fijo habilitado para ese plan — usado por
// SubmitRenewalRequest para no confiar en nada que mande el cliente.
func FindEnabledPlanCycle(views []PlanCycleView, months int) *PlanCycleView {
	for i := range views {
		if views[i].Months == months && views[i].Enabled {
			return &views[i]
		}
	}
	return nil
}
