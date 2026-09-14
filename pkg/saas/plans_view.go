package saas

import "tukifac/pkg/database"

// PublicPlanView — plan visible para el tenant al elegir/renovar. Subconjunto de SaasPlan sin
// campos administrativos (nada que el panel central no quiera que el tenant vea directamente).
type PublicPlanView struct {
	ID                    uint     `json:"id"`
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	Price                 float64  `json:"price"`
	BillingCycle          string   `json:"billing_cycle"`
	IsUnlimitedDocuments  bool     `json:"is_unlimited_documents"`
	MonthlyDocumentsLimit int      `json:"monthly_documents_limit"`
	MaxUsers              int      `json:"max_users"`
	MaxBranches           int      `json:"max_branches"`
	MaxProducts           int      `json:"max_products"`
	Modules               []string `json:"modules"`
	// Cycles: los ciclos fijos (1/3/6/12 meses) habilitados para este plan, con su precio ya
	// calculado (bruto y con descuento aplicado) — el tenant elige uno de acá al renovar por
	// autoservicio, nunca un número de meses libre (eso es exclusivo del panel central).
	Cycles []PlanCycleView `json:"cycles"`
}

// ListActivePlansView devuelve los planes activos para que el tenant elija uno (renovación o
// cambio de plan). Solo activos: los desactivados por el superadmin no deben ofrecerse aunque
// algún tenant viejo siga contratado a ellos (a ese no se le retira nada, ver ReconcileTenant...).
// Solo planes pagados (price > 0): el plan gratuito/trial (ver seed "Trial" en migrations.go) es
// para el alta inicial, no algo que un tenant elija al renovar o cambiar de plan.
func ListActivePlansView() ([]PublicPlanView, error) {
	var plans []database.SaasPlan
	if err := database.CentralDB.Where("active = ? AND price > 0", true).Order("price asc, id asc").Find(&plans).Error; err != nil {
		return nil, err
	}
	out := make([]PublicPlanView, 0, len(plans))
	for _, p := range plans {
		view := PublicPlanView{
			ID:                    p.ID,
			Name:                  p.Name,
			Description:           p.Description,
			Price:                 p.Price,
			BillingCycle:          p.BillingCycle,
			IsUnlimitedDocuments:  p.IsUnlimitedDocuments,
			MonthlyDocumentsLimit: p.MonthlyDocumentsLimit,
			MaxUsers:              p.MaxUsers,
			MaxBranches:           p.MaxBranches,
			MaxProducts:           p.MaxProducts,
			Modules:               []string{},
		}
		var pms []database.SaasPlanModule
		database.CentralDB.Where("plan_id = ?", p.ID).Find(&pms)
		for _, pm := range pms {
			view.Modules = append(view.Modules, pm.ModuleKey)
		}
		view.Cycles = []PlanCycleView{}
		for _, c := range BuildPlanCycleViews(p, LoadPlanCycles(p.ID)) {
			if c.Enabled {
				view.Cycles = append(view.Cycles, c)
			}
		}
		out = append(out, view)
	}
	return out, nil
}
