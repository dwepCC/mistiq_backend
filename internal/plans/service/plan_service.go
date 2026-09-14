package service

import (
	"errors"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/saas"
	"tukifac/pkg/saas/docusage"
)

type PlanService struct{}

func NewPlanService() *PlanService { return &PlanService{} }

type PlanWithModules struct {
	database.SaasPlan
	Modules []string             `json:"modules"`
	Cycles  []saas.PlanCycleView `json:"cycles"`
}

func (s *PlanService) List() ([]PlanWithModules, error) {
	var plans []database.SaasPlan
	if err := database.CentralDB.Order("id asc").Find(&plans).Error; err != nil {
		return nil, err
	}
	result := make([]PlanWithModules, 0, len(plans))
	for _, p := range plans {
		pw := PlanWithModules{SaasPlan: p, Cycles: saas.BuildPlanCycleViews(p, saas.LoadPlanCycles(p.ID))}
		var pms []database.SaasPlanModule
		database.CentralDB.Where("plan_id = ?", p.ID).Find(&pms)
		for _, pm := range pms {
			pw.Modules = append(pw.Modules, pm.ModuleKey)
		}
		if pw.Modules == nil {
			pw.Modules = []string{}
		}
		result = append(result, pw)
	}
	return result, nil
}

func (s *PlanService) GetByID(id uint) (*PlanWithModules, error) {
	var plan database.SaasPlan
	if err := database.CentralDB.First(&plan, id).Error; err != nil {
		return nil, errors.New("plan no encontrado")
	}
	pw := &PlanWithModules{SaasPlan: plan, Modules: []string{}, Cycles: saas.BuildPlanCycleViews(plan, saas.LoadPlanCycles(id))}
	var pms []database.SaasPlanModule
	database.CentralDB.Where("plan_id = ?", id).Find(&pms)
	for _, pm := range pms {
		pw.Modules = append(pw.Modules, pm.ModuleKey)
	}
	return pw, nil
}

type CreatePlanInput struct {
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	Price                 float64  `json:"price"`
	BillingCycle          string   `json:"billing_cycle"`
	Modules               []string `json:"modules"`
	IsUnlimitedDocuments  bool     `json:"is_unlimited_documents"`
	MonthlyDocumentsLimit int      `json:"monthly_documents_limit"`
	// Cuotas del plan (0 = ilimitado).
	MaxUsers    int `json:"max_users"`
	MaxBranches int `json:"max_branches"`
	MaxProducts int `json:"max_products"`
	// Cycles: descuento por cada uno de los 4 ciclos fijos (1/3/6/12 meses) que el tenant puede
	// elegir al renovar por autoservicio. Sin entrada para un months, o vacío, ese ciclo queda
	// sin descuento (precio pleno) pero sigue habilitado — ver saas.SyncPlanCycles.
	Cycles []saas.PlanCycleInput `json:"cycles"`
}

func (s *PlanService) Create(input CreatePlanInput) (*database.SaasPlan, error) {
	if input.Name == "" {
		return nil, errors.New("nombre del plan es requerido")
	}
	if input.BillingCycle == "" {
		input.BillingCycle = "monthly"
	}
	plan := &database.SaasPlan{
		Name: input.Name, Description: input.Description, Price: input.Price,
		BillingCycle: input.BillingCycle, Active: true,
		IsUnlimitedDocuments:  input.IsUnlimitedDocuments,
		MonthlyDocumentsLimit: input.MonthlyDocumentsLimit,
		MaxUsers:              input.MaxUsers,
		MaxBranches:           input.MaxBranches,
		MaxProducts:           input.MaxProducts,
	}
	if err := database.CentralDB.Create(plan).Error; err != nil {
		return nil, err
	}
	s.syncModules(plan.ID, input.Modules)
	saas.SyncPlanCycles(plan.ID, input.Cycles)
	return plan, nil
}

func (s *PlanService) Update(id uint, input CreatePlanInput) error {
	var plan database.SaasPlan
	if err := database.CentralDB.First(&plan, id).Error; err != nil {
		return errors.New("plan no encontrado")
	}
	if input.BillingCycle == "" {
		input.BillingCycle = "monthly"
	}
	database.CentralDB.Model(&plan).Updates(map[string]interface{}{
		"name": input.Name, "description": input.Description, "price": input.Price,
		"billing_cycle":           input.BillingCycle,
		"is_unlimited_documents":  input.IsUnlimitedDocuments,
		"monthly_documents_limit": input.MonthlyDocumentsLimit,
		"max_users":               input.MaxUsers,
		"max_branches":            input.MaxBranches,
		"max_products":            input.MaxProducts,
	})
	s.syncModules(id, input.Modules)
	saas.SyncPlanCycles(id, input.Cycles)
	syncBillingCyclesDocumentQuota(id)
	// Reconciliar módulos de los tenants ya suscritos a este plan (respeta cortesías manuales).
	_ = saas.ReconcileTenantModulesFromPlan(id)
	return nil
}

// syncBillingCyclesDocumentQuota propaga monthly_documents_limit a ciclos abiertos del plan.
func syncBillingCyclesDocumentQuota(planID uint) {
	var cycles []database.SaasBillingCycle
	database.CentralDB.Where("plan_id = ? AND status IN ?", planID,
		[]string{database.SaasInvoicePending, database.SaasInvoiceOverdue, database.SaasInvoicePendingReview}).Find(&cycles)
	for i := range cycles {
		_ = docusage.SyncCycleDocumentQuotaFromPlan(&cycles[i], planID)
	}
}

func (s *PlanService) ToggleActive(id uint) error {
	var plan database.SaasPlan
	if err := database.CentralDB.First(&plan, id).Error; err != nil {
		return errors.New("plan no encontrado")
	}
	return database.CentralDB.Model(&plan).Update("active", !plan.Active).Error
}

func (s *PlanService) Delete(id uint) error {
	var count int64
	database.CentralDB.Model(&database.SaasSubscription{}).Where("plan_id = ?", id).Count(&count)
	if count > 0 {
		return errors.New("no se puede eliminar: el plan tiene suscripciones asociadas")
	}
	database.CentralDB.Where("plan_id = ?", id).Delete(&database.SaasPlanModule{})
	database.CentralDB.Where("plan_id = ?", id).Delete(&database.SaasPlanCycle{})
	return database.CentralDB.Delete(&database.SaasPlan{}, id).Error
}

func (s *PlanService) syncModules(planID uint, keys []string) {
	// Expandir dependencias duras: si el plan incluye 'sales', también incluye 'cashbank', etc.
	// No se fusionan (se venden aparte), pero un plan nunca queda incoherente.
	expanded := expandModuleDeps(keys)
	database.CentralDB.Where("plan_id = ?", planID).Delete(&database.SaasPlanModule{})
	for _, k := range expanded {
		database.CentralDB.Create(&database.SaasPlanModule{PlanID: planID, ModuleKey: k})
	}
}

// expandModuleDeps devuelve las keys más todas sus dependencias transitivas (sin duplicar).
func expandModuleDeps(keys []string) []string {
	seen := map[string]bool{}
	order := []string{}
	var add func(k string)
	add = func(k string) {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		for _, dep := range database.ModuleRequires(k) {
			add(dep)
		}
		order = append(order, k)
	}
	for _, k := range keys {
		add(k)
	}
	return order
}

// ListModules devuelve el catálogo global de módulos (ordenado por sort_order).
func (s *PlanService) ListModules() ([]database.SaasModule, error) {
	modules := make([]database.SaasModule, 0)
	err := database.CentralDB.Order("sort_order asc, id asc").Find(&modules).Error
	return modules, err
}

// ModuleInput: campos editables por el superadmin (presentación). implemented/requires son de
// código (los gestiona SyncModuleCodeMetadata), no se editan aquí.
type ModuleInput struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Category    string `json:"category"`
	SortOrder   int    `json:"sort_order"`
}

func (s *PlanService) CreateModule(in ModuleInput) (*database.SaasModule, error) {
	key := strings.TrimSpace(strings.ToLower(in.Key))
	if key == "" {
		return nil, errors.New("la key del módulo es requerida")
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("el nombre del módulo es requerido")
	}
	var count int64
	database.CentralDB.Model(&database.SaasModule{}).Where("`key` = ?", key).Count(&count)
	if count > 0 {
		return nil, errors.New("ya existe un módulo con esa key")
	}
	category := strings.TrimSpace(in.Category)
	if category == "" {
		category = "core"
	}
	m := &database.SaasModule{
		Key: key, Name: in.Name, Description: in.Description, Icon: in.Icon,
		Category: category, SortOrder: in.SortOrder, Active: true,
	}
	if err := database.CentralDB.Create(m).Error; err != nil {
		return nil, err
	}
	return m, nil
}

func (s *PlanService) UpdateModule(id uint, in ModuleInput) error {
	var m database.SaasModule
	if err := database.CentralDB.First(&m, id).Error; err != nil {
		return errors.New("módulo no encontrado")
	}
	category := strings.TrimSpace(in.Category)
	if category == "" {
		category = m.Category
	}
	// La key NO se cambia (romperría planes/tenants que la referencian); solo presentación.
	return database.CentralDB.Model(&m).Updates(map[string]interface{}{
		"name": in.Name, "description": in.Description, "icon": in.Icon,
		"category": category, "sort_order": in.SortOrder,
	}).Error
}

func (s *PlanService) ToggleModule(id uint) error {
	var m database.SaasModule
	if err := database.CentralDB.First(&m, id).Error; err != nil {
		return errors.New("módulo no encontrado")
	}
	return database.CentralDB.Model(&m).Update("active", !m.Active).Error
}

func (s *PlanService) DeleteModule(id uint) error {
	var m database.SaasModule
	if err := database.CentralDB.First(&m, id).Error; err != nil {
		return errors.New("módulo no encontrado")
	}
	if database.IsCodeModule(m.Key) {
		return errors.New("no se puede eliminar un módulo del sistema; desactívalo en su lugar")
	}
	var used int64
	database.CentralDB.Model(&database.SaasPlanModule{}).Where("module_key = ?", m.Key).Count(&used)
	if used > 0 {
		return errors.New("el módulo está asignado a uno o más planes; quítalo de los planes primero")
	}
	database.CentralDB.Where("module_key = ?", m.Key).Delete(&database.TenantModule{})
	return database.CentralDB.Delete(&database.SaasModule{}, id).Error
}
