package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	usersvc "tukifac/internal/users/service"
	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/database/engine"
	"tukifac/pkg/middleware"
	"tukifac/pkg/saas"
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
	SubscriptionMonths int    `json:"subscription_months"` // duración en meses de la suscripción al crear (0 = no crear suscripción automática)
	Rubro              string `json:"rubro"`               // general | gastronomico
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

	plan := strings.TrimSpace(input.Plan)
	if plan == "" {
		plan = "trial"
	}
	months := input.SubscriptionMonths
	if months <= 0 {
		months = 1
	}

	dbName := "saas_tenant_" + slug
	trialEnd := time.Now().AddDate(0, 0, 30)
	rubro := tenantrubro.Normalize(input.Rubro)
	tenant = &database.Tenant{
		Name: input.Name, Slug: slug, DBName: dbName, Plan: plan, Status: "active",
		Email: input.Email, Phone: input.Phone, RUC: input.RUC, Rubro: rubro,
		Address: input.Address, Ubigeo: input.Ubigeo, TrialEndsAt: &trialEnd,
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
		Rubro: rubro,
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

func (s *TenantService) List(query, status, regionID, provinciaID string) ([]database.Tenant, error) {
	var tenants []database.Tenant
	q := s.db.Model(&database.Tenant{})
	if query != "" {
		q = q.Where("name LIKE ? OR slug LIKE ? OR ruc LIKE ? OR address LIKE ?",
			"%"+query+"%", "%"+query+"%", "%"+query+"%", "%"+query+"%")
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	// Filtro por departamento: ubigeo empieza con los 2 primeros dígitos del region_id (ej. 15 para Lima)
	if regionID != "" && len(regionID) >= 2 {
		prefix := regionID
		if len(prefix) > 2 {
			prefix = prefix[:2]
		}
		q = q.Where("ubigeo LIKE ?", prefix+"%")
	}
	// Filtro por provincia: ubigeo empieza con los 4 primeros dígitos del provincia_id (ej. 1501 para Lima)
	if provinciaID != "" && len(provinciaID) >= 4 {
		prefix := provinciaID[:4]
		q = q.Where("ubigeo LIKE ?", prefix+"%")
	}
	err := q.Order("created_at DESC").Find(&tenants).Error
	return tenants, err
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

func (s *TenantService) GetByID(id uint) (*database.Tenant, error) {
	var tenant database.Tenant
	if err := s.db.First(&tenant, id).Error; err != nil {
		return nil, err
	}
	return &tenant, nil
}

func (s *TenantService) Update(id uint, input database.Tenant) error {
	// Obtener plan actual para detectar si cambió
	var current database.Tenant
	s.db.Select("plan").First(&current, id)

	err := s.db.Model(&database.Tenant{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":    input.Name,
		"email":   input.Email,
		"phone":   input.Phone,
		"ruc":     input.RUC,
		"plan":    input.Plan,
		"status":  input.Status,
		"address": input.Address,
		"ubigeo":  input.Ubigeo,
	}).Error
	if err != nil {
		return err
	}

	// Si el plan cambió, sincronizar módulos automáticamente
	if input.Plan != "" && input.Plan != current.Plan {
		s.syncModulesByPlanName(id, input.Plan)
	}

	var updated database.Tenant
	if err := s.db.Select("slug").First(&updated, id).Error; err == nil {
		middleware.InvalidateTenantCache(updated.Slug)
	}
	return nil
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

func (s *TenantService) SetModule(tenantID uint, moduleKey string, enabled bool) error {
	var mod database.TenantModule
	err := s.db.Where("tenant_id = ? AND module_key = ?", tenantID, moduleKey).First(&mod).Error
	if err != nil {
		// Crear
		return s.db.Create(&database.TenantModule{
			TenantID:   tenantID,
			ModuleKey:  moduleKey,
			Enabled:    enabled,
			ConfigJSON: strPtr("{}"),
		}).Error
	}
	return s.db.Model(&mod).Update("enabled", enabled).Error
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

func (s *TenantService) Stats() (map[string]int64, error) {
	stats := make(map[string]int64)
	var total, active, inactive, trial, basic, pro int64
	s.db.Model(&database.Tenant{}).Count(&total)
	s.db.Model(&database.Tenant{}).Where("status = ?", "active").Count(&active)
	s.db.Model(&database.Tenant{}).Where("status = ?", "inactive").Count(&inactive)
	s.db.Model(&database.Tenant{}).Where("plan = ?", "trial").Count(&trial)
	s.db.Model(&database.Tenant{}).Where("plan = ?", "basic").Count(&basic)
	s.db.Model(&database.Tenant{}).Where("plan = ?", "pro").Count(&pro)
	stats["total"] = total
	stats["active"] = active
	stats["inactive"] = inactive
	stats["trial"] = trial
	stats["basic"] = basic
	stats["pro"] = pro
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
