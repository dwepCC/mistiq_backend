package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/config"
	authsvc "tukifac/internal/auth/service"
	restaurantsvc "tukifac/internal/restaurant/service"
	usersvc "tukifac/internal/users/service"
	"tukifac/pkg/database"
	"tukifac/pkg/database/engine"
	"tukifac/pkg/database/tenantbackfills"
	"tukifac/pkg/domains"
	"tukifac/pkg/middleware"
	"tukifac/pkg/pagination"
	"tukifac/pkg/saas"
	"tukifac/pkg/taxregime"
	"tukifac/pkg/tenantrubro"
	"tukifac/pkg/tenantstorage"
	"tukifac/pkg/utils"

	"gorm.io/gorm"
)

type TenantService struct {
	db *gorm.DB
}

func strPtr(s string) *string { return &s }

func NewTenantService() *TenantService {
	return &TenantService{db: database.CentralDB}
}

type CreateTenantInput struct {
	Name               string `json:"name"`
	Email              string `json:"email"`
	Phone              string `json:"phone"`
	RUC                string `json:"ruc"`
	Plan               string `json:"plan"`
	Slug               string `json:"slug"`
	Address            string `json:"address"`
	Ubigeo             string `json:"ubigeo"` // código 6 dígitos del distrito
	AdminEmail         string `json:"admin_email"`
	AdminPassword      string `json:"admin_password"`
	// SubscriptionMonths duración de la suscripción inicial. Toda empresa nace con una
	// suscripción —las gratuitas también, vinculadas al plan gratis—, así que 0 se corrige a 1
	// en vez de saltarse el alta.
	SubscriptionMonths int    `json:"subscription_months"`
	Rubro              string `json:"rubro"`           // general | gastronomico
	TaxpayerRegime     string `json:"taxpayer_regime"` // general | nrus — régimen tributario del contribuyente
	// Descuento opcional sobre el cobro inicial (precio del plan × meses).
	DiscountType  string  `json:"discount_type"`
	DiscountValue float64 `json:"discount_value"`
}

// Create provisioning completo y transaccional (rollback automático si falla cualquier paso).
func (s *TenantService) Create(input CreateTenantInput) (tenant *database.Tenant, err error) {
	if input.Name == "" {
		return nil, errors.New("el nombre es requerido")
	}
	if input.AdminEmail == "" || input.AdminPassword == "" {
		return nil, errors.New("email y contraseña del administrador son requeridos")
	}

	slug, err := resolveCreateSlug(input)
	if err != nil {
		return nil, err
	}
	if config.AppConfig != nil && config.AppConfig.IsReservedSubdomain(slug) {
		return nil, fmt.Errorf("el subdominio %q está reservado (api, app, www, etc.). Elige otro identificador", slug)
	}
	var slugCount int64
	if err = s.db.Unscoped().Model(&database.Tenant{}).Where("slug = ?", slug).Count(&slugCount).Error; err != nil {
		return nil, err
	}
	if slugCount > 0 {
		return nil, errors.New("ya existe una empresa con ese subdominio. Elige otro identificador")
	}

	// Sin default: el catálogo de planes lo define el superadmin y no tiene por qué existir
	// uno llamado «trial». Antes se asumía ese nombre y el alta fallaba con un error opaco de
	// plan no encontrado en vez de decir que faltaba elegirlo.
	plan := strings.TrimSpace(input.Plan)
	if plan == "" {
		return nil, errors.New("debe seleccionar un plan para la empresa")
	}
	months := input.SubscriptionMonths
	if months <= 0 {
		months = 1
	}

	dbName := "saas_tenant_" + slug
	trialEnd := time.Now().AddDate(0, 0, 30)
	rubro := tenantrubro.Normalize(input.Rubro)
	regime := string(taxregime.Normalize(input.TaxpayerRegime))
	tenant = &database.Tenant{
		Name: input.Name, Slug: slug, DBName: dbName, Plan: plan, Status: "active",
		Email: input.Email, Phone: input.Phone, RUC: input.RUC, Rubro: rubro,
		TaxpayerRegime: regime,
		Address:        input.Address, Ubigeo: input.Ubigeo, TrialEndsAt: &trialEnd,
	}

	defer func() {
		if err != nil {
			database.RollbackTenantProvision(s.db, tenant.ID, dbName)
		}
	}()

	// 1. Tenant central
	if err = s.db.Create(tenant).Error; err != nil {
		return nil, fmt.Errorf("creando tenant central: %w", err)
	}
	middleware.InvalidateTenantCache(slug)

	seedIn := database.TenantSeedInput{
		AdminEmail: input.AdminEmail, AdminPassword: input.AdminPassword,
		CompanyName: input.Name, RUC: input.RUC,
		Address: input.Address, Ubigeo: input.Ubigeo,
		Phone: input.Phone, Email: input.Email,
		Rubro:          rubro,
		TaxpayerRegime: regime,
	}

	// 2–4. BD vacía + migraciones versionadas + seed
	if err = engine.ProvisionTenantDB(dbName, tenant.ID, slug, seedIn); err != nil {
		return nil, err
	}

	tenantDB, err := database.GetTenantDB(dbName)
	if err != nil {
		return nil, fmt.Errorf("conectando BD tenant: %w", err)
	}
	defer database.ReleaseTenantDB(dbName)

	roleSvc := usersvc.NewRoleService(tenantDB)
	if err = roleSvc.SeedPermissions(); err != nil {
		return nil, fmt.Errorf("inicializando permisos: %w", err)
	}
	perms, err := roleSvc.AllPermissions()
	if err != nil {
		return nil, fmt.Errorf("listando permisos: %w", err)
	}
	var adminRole database.TenantRole
	if err = tenantDB.Where("name = ?", "Administrador").First(&adminRole).Error; err != nil {
		return nil, fmt.Errorf("rol Administrador: %w", err)
	}
	permIDs := make([]uint, len(perms))
	for i, p := range perms {
		permIDs[i] = p.ID
	}
	if err = roleSvc.SetRolePermissions(adminRole.ID, permIDs); err != nil {
		return nil, fmt.Errorf("asignando permisos al Administrador: %w", err)
	}

	// 5–6. Suscripción + billing cycle (módulos según plan vía syncTenantModulesFromPlanTx)
	if _, err = saas.ProvisionInitialSubscription(
		tenant.ID, plan, months, "Suscripción creada al registrar la empresa",
		saas.Discount{Type: input.DiscountType, Value: input.DiscountValue},
	); err != nil {
		return nil, fmt.Errorf("suscripción SaaS: %w", err)
	}

	if tenantrubro.IsGastronomico(rubro) {
		if err = database.EnableTenantModule(s.db, tenant.ID, "restaurant"); err != nil {
			return nil, fmt.Errorf("activando módulo restaurante: %w", err)
		}
	}

	saas.InvalidateTenantCache(tenant.ID)
	return tenant, nil
}

func resolveCreateSlug(input CreateTenantInput) (string, error) {
	if input.Slug != "" {
		return utils.NormalizeSubdomain(input.Slug)
	}
	slug := utils.Slugify(input.Name)
	if slug == "" {
		return "", errors.New("nombre inválido para generar subdominio")
	}
	slug = strings.ReplaceAll(slug, "-", "")
	if len(slug) < 2 {
		slug = utils.Slugify(input.Name)
	}
	return slug, nil
}

// rollbackNewTenant revierte alta parcial (purge central hard delete + drop BD).
func (s *TenantService) rollbackNewTenant(tenantID uint, dbName string) {
	database.RollbackTenantProvision(s.db, tenantID, dbName)
}

// TenantListParams filtros y paginación de empresas.
type TenantListParams struct {
	Query       string
	Status      string
	RegionID    string
	ProvinciaID string
	Page        int
	PerPage     int
}

func (s *TenantService) applyTenantFilters(q *gorm.DB, query, status, regionID, provinciaID string) *gorm.DB {
	if query != "" {
		q = q.Where("name LIKE ? OR slug LIKE ? OR ruc LIKE ? OR address LIKE ? OR email LIKE ?",
			"%"+query+"%", "%"+query+"%", "%"+query+"%", "%"+query+"%", "%"+query+"%")
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if regionID != "" && len(regionID) >= 2 {
		prefix := regionID
		if len(prefix) > 2 {
			prefix = prefix[:2]
		}
		q = q.Where("ubigeo LIKE ?", prefix+"%")
	}
	if provinciaID != "" && len(provinciaID) >= 4 {
		prefix := provinciaID[:4]
		q = q.Where("ubigeo LIKE ?", prefix+"%")
	}
	return q
}

// List devuelve tenants paginados (LIMIT/OFFSET en BD).
func (s *TenantService) List(params TenantListParams) ([]database.Tenant, int64, error) {
	page, perPage := pagination.Normalize(params.Page, params.PerPage)
	q := s.applyTenantFilters(s.db.Model(&database.Tenant{}), params.Query, params.Status, params.RegionID, params.ProvinciaID)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var tenants []database.Tenant
	err := q.Order("created_at DESC").
		Limit(perPage).
		Offset(pagination.Offset(page, perPage)).
		Find(&tenants).Error
	return tenants, total, err
}

// BillingEnabledByTenantIDs devuelve un mapa tenant_id -> true si el tenant tiene el módulo billing habilitado.
func (s *TenantService) BillingEnabledByTenantIDs(ids []uint) (map[uint]bool, error) {
	if len(ids) == 0 {
		return map[uint]bool{}, nil
	}
	var mods []database.TenantModule
	if err := s.db.Where("tenant_id IN ? AND module_key = ? AND enabled = ?", ids, "billing", true).Find(&mods).Error; err != nil {
		return nil, err
	}
	out := make(map[uint]bool)
	for _, m := range mods {
		out[m.TenantID] = true
	}
	return out, nil
}

// TenantPlanRef identifica el plan de un tenant contra el catálogo SaaS.
// La columna tenants.plan es texto libre; el vínculo real es saas_subscriptions.plan_id.
type TenantPlanRef struct {
	PlanID   uint   `json:"plan_id"`
	PlanName string `json:"plan_name"`
}

// PlanRefsByTenantIDs resuelve el plan de cada tenant en dos consultas (sin N+1).
//
// Prioriza la suscripción vigente, que es la fuente de verdad del SaaS; si el tenant no
// tiene ninguna (datos antiguos), cae al nombre guardado en tenants.plan resuelto contra
// el catálogo. Sin esto el panel no puede preseleccionar el plan por identidad.
func (s *TenantService) PlanRefsByTenantIDs(ids []uint, planNames map[uint]string) (map[uint]TenantPlanRef, error) {
	out := make(map[uint]TenantPlanRef)
	if len(ids) == 0 {
		return out, nil
	}

	var plans []database.SaasPlan
	if err := s.db.Find(&plans).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]database.SaasPlan, len(plans))
	byLowerName := make(map[string]database.SaasPlan, len(plans))
	for _, p := range plans {
		byID[p.ID] = p
		byLowerName[strings.ToLower(strings.TrimSpace(p.Name))] = p
	}

	var subs []database.SaasSubscription
	if err := s.db.Where("tenant_id IN ?", ids).
		Where("status NOT IN ?", []string{database.SaasSubCancelled}).
		Order("created_at desc").Find(&subs).Error; err != nil {
		return nil, err
	}
	for _, sub := range subs {
		if _, seen := out[sub.TenantID]; seen {
			continue // Order desc: la primera es la vigente.
		}
		if p, ok := byID[sub.PlanID]; ok {
			out[sub.TenantID] = TenantPlanRef{PlanID: p.ID, PlanName: p.Name}
		}
	}

	// Respaldo por nombre para tenants sin suscripción registrada.
	for _, id := range ids {
		if _, ok := out[id]; ok {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(planNames[id]))
		if p, ok := byLowerName[name]; ok {
			out[id] = TenantPlanRef{PlanID: p.ID, PlanName: p.Name}
		}
	}
	return out, nil
}

func (s *TenantService) GetByID(id uint) (*database.Tenant, error) {
	var tenant database.Tenant
	if err := s.db.First(&tenant, id).Error; err != nil {
		return nil, err
	}
	return &tenant, nil
}

func (s *TenantService) Update(id uint, input database.Tenant) error {
	// Obtener plan y db actuales (plan para detectar cambio; db_name para propagar el régimen).
	var current database.Tenant
	s.db.Select("plan", "db_name").First(&current, id)

	// El plan se valida contra el catálogo antes de escribir nada: antes se guardaba
	// cualquier texto y syncModulesByPlanName fallaba en silencio, dejando al tenant con
	// un plan inexistente y sin módulos actualizados.
	var newPlan *database.SaasPlan
	if strings.TrimSpace(input.Plan) != "" {
		var plan database.SaasPlan
		if err := s.db.Where("LOWER(name) = LOWER(?)", strings.TrimSpace(input.Plan)).
			First(&plan).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("el plan %q no existe en el catálogo SaaS", input.Plan)
			}
			return err
		}
		if !plan.Active {
			return fmt.Errorf("el plan %q está inactivo", plan.Name)
		}
		newPlan = &plan
	}

	updates := map[string]interface{}{
		"name":    input.Name,
		"email":   input.Email,
		"phone":   input.Phone,
		"ruc":     input.RUC,
		"status":  input.Status,
		"address": input.Address,
		"ubigeo":  input.Ubigeo,
	}
	if newPlan != nil {
		// Nombre canónico del catálogo, no el que mandó el formulario: así deja de haber
		// mezcla de mayúsculas ("Basic" vs "basic") en la misma columna.
		updates["plan"] = newPlan.Name
	}
	// Régimen tributario: se persiste en el tenant central y se propaga a la config
	// del tenant (fuente que lee el gate de emisión y las capabilities del frontend).
	regime := ""
	if strings.TrimSpace(input.TaxpayerRegime) != "" {
		regime = string(taxregime.Normalize(input.TaxpayerRegime))
		updates["taxpayer_regime"] = regime
	}
	err := s.db.Model(&database.Tenant{}).Where("id = ?", id).Updates(updates).Error
	if err != nil {
		return err
	}
	if regime != "" && strings.TrimSpace(current.DBName) != "" {
		if tdb, e := database.GetTenantDB(current.DBName); e == nil {
			_ = tdb.Model(&database.TenantCompanyConfig{}).Where("id > 0").
				Update("taxpayer_regime", regime).Error
			database.ReleaseTenantDB(current.DBName)
		}
	}

	// Si el plan cambió: módulos y suscripción vigente. Sin repuntar la suscripción, la
	// vista de Empresas mostraba un plan y la de Suscripciones otro, porque el vínculo
	// real (saas_subscriptions.plan_id) se quedaba en el plan anterior.
	if newPlan != nil && !strings.EqualFold(newPlan.Name, current.Plan) {
		s.syncModulesByPlanName(id, newPlan.Name)
		if err := s.repointActiveSubscription(id, newPlan.ID); err != nil {
			return err
		}
	}

	var updated database.Tenant
	if err := s.db.Select("slug").First(&updated, id).Error; err == nil {
		middleware.InvalidateTenantCache(updated.Slug)
	}
	return nil
}

// repointActiveSubscription apunta la suscripción vigente al nuevo plan.
//
// Solo cambia plan_id: fechas, ciclo y estado se conservan, porque cambiar el plan desde
// Empresas es una corrección administrativa, no un cobro. Si el tenant no tiene ninguna
// suscripción no se inventa una: ese alta pertenece al flujo de pagos.
func (s *TenantService) repointActiveSubscription(tenantID, planID uint) error {
	var sub database.SaasSubscription
	err := s.db.Where("tenant_id = ?", tenantID).
		Where("status NOT IN ?", []string{database.SaasSubCancelled}).
		Order("created_at desc").First(&sub).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if sub.PlanID == planID {
		return nil
	}
	return s.db.Model(&database.SaasSubscription{}).
		Where("id = ?", sub.ID).
		Update("plan_id", planID).Error
}

// syncModulesByPlanName encuentra el SaasPlan correspondiente al nombre y sincroniza TenantModule
func (s *TenantService) syncModulesByPlanName(tenantID uint, planName string) {
	var plan database.SaasPlan
	// Buscar plan por nombre (case-insensitive)
	if err := s.db.Where("LOWER(name) = LOWER(?)", planName).First(&plan).Error; err != nil {
		return // Plan no encontrado en saas_plans, omitir sincronización
	}

	var planModules []database.SaasPlanModule
	s.db.Where("plan_id = ?", plan.ID).Find(&planModules)

	planSet := make(map[string]bool)
	for _, pm := range planModules {
		planSet[pm.ModuleKey] = true
	}

	// Desactivar todos los módulos del tenant primero
	s.db.Model(&database.TenantModule{}).
		Where("tenant_id = ?", tenantID).
		Update("enabled", false)

	// Activar sólo los módulos del nuevo plan
	for key := range planSet {
		var tm database.TenantModule
		if err := s.db.Where("tenant_id = ? AND module_key = ?", tenantID, key).First(&tm).Error; err != nil {
			s.db.Create(&database.TenantModule{
				TenantID:   tenantID,
				ModuleKey:  key,
				Enabled:    true,
				ConfigJSON: strPtr("{}"),
			})
		} else {
			s.db.Model(&tm).Update("enabled", true)
		}
	}
}

func (s *TenantService) SetStatus(id uint, status string) error {
	if status == "inactive" {
		var tenant database.Tenant
		if err := s.db.First(&tenant, id).Error; err != nil {
			return err
		}
		database.RemoveTenantFromPool(tenant.DBName)
	}
	return s.db.Model(&database.Tenant{}).Where("id = ?", id).Update("status", status).Error
}

func (s *TenantService) GetModules(tenantID uint) ([]database.TenantModule, error) {
	var modules []database.TenantModule
	err := s.db.Where("tenant_id = ?", tenantID).Find(&modules).Error
	return modules, err
}

// SetModule: activación/desactivación MANUAL de un módulo a un tenant por el superadmin
// (cortesía o revocación puntual). Marca source='manual' para que la decisión sobreviva a la
// reconciliación con el plan (ver saas.ReconcileTenantModulesFromPlan).
func (s *TenantService) SetModule(tenantID uint, moduleKey string, enabled bool) error {
	var mod database.TenantModule
	err := s.db.Where("tenant_id = ? AND module_key = ?", tenantID, moduleKey).First(&mod).Error
	if err != nil {
		return s.db.Create(&database.TenantModule{
			TenantID:   tenantID,
			ModuleKey:  moduleKey,
			Enabled:    enabled,
			Source:     "manual",
			ConfigJSON: strPtr("{}"),
		}).Error
	}
	if e := s.db.Model(&mod).Updates(map[string]interface{}{"enabled": enabled, "source": "manual"}).Error; e != nil {
		return e
	}
	saas.InvalidateTenantCache(tenantID)
	return nil
}

// RunMigrations ejecuta las migraciones del tenant (tablas/columnas nuevas) y opcionalmente el seed de permisos si no existen.
func (s *TenantService) RunMigrations(tenantID uint) error {
	tenant, err := s.GetByID(tenantID)
	if err != nil {
		return err
	}
	if err := engine.MigrateTenantIncremental(tenant.ID, tenant.Slug, tenant.DBName); err != nil {
		return fmt.Errorf("migrando esquema: %w", err)
	}
	tenantDB, err := database.GetTenantDB(tenant.DBName)
	if err != nil {
		return fmt.Errorf("conectando BD del tenant: %w", err)
	}
	// Asegurar que permisos existan (tenants creados antes de este cambio pueden no tenerlos)
	roleSvc := usersvc.NewRoleService(tenantDB)
	if err := roleSvc.SeedPermissions(); err != nil {
		return fmt.Errorf("inicializando permisos: %w", err)
	}
	var adminRole database.TenantRole
	if err := tenantDB.Where("name = ?", "Administrador").First(&adminRole).Error; err == nil {
		perms, _ := roleSvc.AllPermissions()
		permIDs := make([]uint, len(perms))
		for i, p := range perms {
			permIDs[i] = p.ID
		}
		_ = roleSvc.SetRolePermissions(adminRole.ID, permIDs)
	}
	return nil
}

// RunMigrationsAll ejecuta migraciones para todos los tenants activos (no detiene en el primero fallido).
func (s *TenantService) RunMigrationsAll() (database.MigrateSummary, error) {
	esummary := engine.MigrateTenantsBatch(true, nil)
	summary := database.MigrateSummary{Success: esummary.Success, Failed: esummary.Failed}
	for _, f := range summary.Failed {
		if f.Slug == "(list)" {
			return summary, f.Err
		}
	}
	return summary, nil
}

// BackfillInfo describe un backfill disponible para el panel central.
type BackfillInfo struct {
	Version     int    `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ListBackfills backfills registrados (run-once, idempotentes).
func (s *TenantService) ListBackfills() []BackfillInfo {
	out := make([]BackfillInfo, 0, len(tenantbackfills.TenantBackfills))
	for _, b := range tenantbackfills.TenantBackfills {
		out = append(out, BackfillInfo{
			Version:     b.Version(),
			Name:        b.Name(),
			Description: tenantbackfills.DescriptionOf(b),
		})
	}
	return out
}

// RunBackfill ejecuta un backfill en un tenant. version <= 0 = todos los registrados.
// Idempotente: el runner salta los ya aplicados en ese tenant.
func (s *TenantService) RunBackfill(tenantID uint, version int) error {
	tenant, err := s.GetByID(tenantID)
	if err != nil {
		return err
	}
	summary := engine.RunBackfillFleet(engine.BackfillOptions{
		Version:    version,
		TenantSlug: tenant.Slug,
		Workers:    1,
	})
	for _, f := range summary.Failed {
		return f.Err
	}
	return nil
}

// RunBackfillAll ejecuta un backfill en toda la flota. version <= 0 = todos los registrados.
func (s *TenantService) RunBackfillAll(version int) database.MigrateSummary {
	esummary := engine.RunBackfillFleet(engine.BackfillOptions{
		Version:    version,
		ActiveOnly: true,
	})
	return database.MigrateSummary{Success: esummary.Success, Failed: esummary.Failed}
}

// CleanupAbandonedQuickSales cancela las ventas rápidas abandonadas de un tenant y saca sus
// comandas de la cocina. Re-ejecutable (mantenimiento recurrente). Devuelve cuántas se
// cancelaron.
func (s *TenantService) CleanupAbandonedQuickSales(tenantID uint) (int64, error) {
	tenant, err := s.GetByID(tenantID)
	if err != nil {
		return 0, err
	}
	tdb, err := database.GetTenantDB(tenant.DBName)
	if err != nil {
		return 0, fmt.Errorf("conectando BD del tenant: %w", err)
	}
	defer database.ReleaseTenantDB(tenant.DBName)
	return restaurantsvc.New(tdb).CleanupAbandonedQuickSales()
}

// CleanupAbandonedQuickSalesFleet corre la limpieza en todos los tenants activos.
// No se detiene en el primero que falle; reporta el total cancelado y los que fallaron.
func (s *TenantService) CleanupAbandonedQuickSalesFleet() (cleaned int64, failed []string) {
	var tenants []database.Tenant
	s.db.Where("status = ?", "active").Find(&tenants)
	for _, t := range tenants {
		tdb, err := database.GetTenantDB(t.DBName)
		if err != nil {
			failed = append(failed, t.Slug)
			continue
		}
		n, err := restaurantsvc.New(tdb).CleanupAbandonedQuickSales()
		database.ReleaseTenantDB(t.DBName)
		if err != nil {
			failed = append(failed, t.Slug)
			continue
		}
		cleaned += n
	}
	return cleaned, failed
}

func (s *TenantService) Stats() (map[string]int64, error) {
	stats := make(map[string]int64)
	var total, active, inactive int64
	s.db.Model(&database.Tenant{}).Count(&total)
	s.db.Model(&database.Tenant{}).Where("status = ?", "active").Count(&active)
	s.db.Model(&database.Tenant{}).Where("status = ?", "inactive").Count(&inactive)
	stats["total"] = total
	stats["active"] = active
	stats["inactive"] = inactive

	// Conteo por plan REAL (suscripción vigente), no por la columna legacy tenants.plan que se
	// desincroniza. Dinámico: una clave "plan_<nombre>" por cada plan del catálogo.
	planName := map[uint]string{}
	var plans []database.SaasPlan
	s.db.Find(&plans)
	for _, p := range plans {
		planName[p.ID] = p.Name
	}
	var tenants []database.Tenant
	s.db.Select("id").Find(&tenants)
	byPlan := map[string]int64{}
	for _, t := range tenants {
		var sub database.SaasSubscription
		if err := s.db.Where("tenant_id = ?", t.ID).
			Where("status NOT IN ?", []string{database.SaasSubCancelled}).
			Order("created_at desc").First(&sub).Error; err != nil {
			byPlan["sin_plan"]++
			continue
		}
		name := strings.ToLower(strings.TrimSpace(planName[sub.PlanID]))
		if name == "" {
			name = "desconocido"
		}
		byPlan[name]++
	}
	for name, cnt := range byPlan {
		stats["plan_"+name] = cnt
	}
	// Compat: claves legacy que el dashboard aún consume (se migran a plan_* en Fase 2).
	stats["trial"] = byPlan["trial"]
	stats["basic"] = byPlan["basic"]
	stats["pro"] = byPlan["pro"]
	return stats, nil
}

// DestroyTenantInput cuerpo para eliminación completa (clave de operaciones + confirmación de slug).
type DestroyTenantInput struct {
	OperationsKey string `json:"operations_key"`
	ConfirmSlug   string `json:"confirm_slug"`
}

// DestroyTenantResult resumen de la eliminación física.
type DestroyTenantResult struct {
	TenantID      uint     `json:"tenant_id"`
	Slug          string   `json:"slug"`
	DBName        string   `json:"db_name"`
	DBDropped     bool     `json:"db_dropped"`
	CentralPurged bool     `json:"central_purged"`
	PathsRemoved  []string `json:"paths_removed"`
	FileErrors    []string `json:"file_errors,omitempty"`
}

// DestroyTenantComplete elimina BD tenant, datos centrales y archivos locales (no toca Lycet/SUNAT externo).
func (s *TenantService) DestroyTenantComplete(id uint, input DestroyTenantInput) (*DestroyTenantResult, error) {
	if err := saas.VerifyOperationsKey(input.OperationsKey); err != nil {
		return nil, err
	}
	var tenant database.Tenant
	if err := s.db.Unscoped().First(&tenant, id).Error; err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.ConfirmSlug) != tenant.Slug {
		return nil, errors.New("confirm_slug no coincide con el subdominio del tenant")
	}

	res := &DestroyTenantResult{
		TenantID: tenant.ID,
		Slug:     tenant.Slug,
		DBName:   tenant.DBName,
	}

	database.RemoveTenantFromPool(tenant.DBName)
	if err := database.DropTenantDB(tenant.DBName); err != nil {
		return nil, fmt.Errorf("eliminar BD tenant: %w", err)
	}
	res.DBDropped = true

	invBase := ""
	if config.AppConfig != nil {
		invBase = config.AppConfig.InvoiceStoragePath
	}
	removed, fileErrs := tenantstorage.RemoveAllTenantFiles(tenant.ID, tenant.RUC, invBase)
	res.PathsRemoved = removed
	for _, e := range fileErrs {
		res.FileErrors = append(res.FileErrors, e.Error())
	}

	if err := database.PurgeTenantCentralData(s.db, tenant.ID); err != nil {
		return res, fmt.Errorf("purge central: %w", err)
	}
	res.CentralPurged = true
	middleware.InvalidateTenantCache(tenant.Slug)
	saas.InvalidateTenantCache(tenant.ID)
	return res, nil
}

// MasterAccessResult respuesta del acceso maestro al ERP web del tenant.
type MasterAccessResult struct {
	TenantURL string
	Token     string
}

// MasterAccess genera JWT del usuario propietario del tenant para soporte técnico (ERP web).
func (s *TenantService) MasterAccess(tenantID, saUserID uint, saEmail, clientIP string) (*MasterAccessResult, error) {
	tenant, err := s.GetByID(tenantID)
	if err != nil {
		return nil, errors.New("tenant no encontrado")
	}

	tenantDB, err := database.GetTenantDB(tenant.DBName)
	if err != nil {
		return nil, fmt.Errorf("conectando BD tenant: %w", err)
	}
	defer database.ReleaseTenantDB(tenant.DBName)

	ownerID, ok, err := database.TenantOwnerUserID(tenantDB)
	if err != nil {
		return nil, fmt.Errorf("obteniendo usuario propietario: %w", err)
	}
	if !ok || ownerID == 0 {
		return nil, errors.New("no se encontró el usuario propietario del tenant")
	}

	user, legacyBranch, err := database.LoadTenantUserForBranch(tenantDB, ownerID)
	if err != nil {
		return nil, errors.New("usuario propietario no encontrado")
	}
	if !user.Active {
		return nil, errors.New("el usuario propietario está inactivo")
	}

	session, err := authsvc.BuildTenantSession(tenant, tenantDB, user, legacyBranch, authsvc.TenantSessionOpts{
		AuthMethod:       middleware.AuthMethodMasterAccess,
		Impersonated:     true,
		MasterActorID:    saUserID,
		MasterActorEmail: saEmail,
	})
	if err != nil {
		return nil, fmt.Errorf("generando sesión: %w", err)
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"superadmin_id":    saUserID,
		"superadmin_email": saEmail,
		"tenant_slug":      tenant.Slug,
		"owner_user_id":    user.ID,
		"owner_email":      user.Email,
	})
	_ = s.db.Create(&database.AuditLog{
		TenantID:  tenant.ID,
		UserID:    saUserID,
		Action:    "master_access",
		Entity:    "tenant_user",
		EntityID:  user.ID,
		Payload:   string(payload),
		IPAddress: clientIP,
	}).Error

	rootDomain := ""
	if config.AppConfig != nil {
		rootDomain = config.AppConfig.AppDomain
	}
	return &MasterAccessResult{
		TenantURL: domains.TenantURL(tenant.Slug, rootDomain),
		Token:     session.Token,
	}, nil
}
