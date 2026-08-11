package service

import (
	"errors"
	"fmt"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/database/engine"
	"tukifac/pkg/migrationalert"

	"gorm.io/gorm"
)

// MigrationFleetService operaciones del dashboard de migraciones.
type MigrationFleetService struct{}

func NewMigrationFleetService() *MigrationFleetService {
	return &MigrationFleetService{}
}

// MigrationListItem fila del listado.
type MigrationListItem struct {
	TenantID        uint       `json:"tenant_id"`
	TenantSlug      string     `json:"tenant_slug"`
	CompanyName     string     `json:"company_name"`
	CurrentVersion  int        `json:"current_version"`
	TargetVersion   int        `json:"target_version"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	LastError       *string    `json:"last_error"`
	LastMigratedAt     *time.Time `json:"last_migrated_at"`
	Outdated           bool       `json:"outdated"`
	MigrationsApplied  int        `json:"migrations_applied"`
	MigrationsPending  int        `json:"migrations_pending"`
}

// MigrationListParams filtros listado.
type MigrationListParams struct {
	Page           int
	PerPage        int
	Status         string
	CurrentVersion int
	TargetVersion  int
	Outdated       bool
	Failed         bool
	Pending        bool
	TenantSlug         string
	TenantName         string
	Drifted            bool
	LastMigratedFrom   string
	LastMigratedTo     string
}

// MigrationSummary resumen global.
type MigrationSummary struct {
	Total               int64  `json:"total"`
	Completed           int64  `json:"completed"`
	Pending             int64  `json:"pending"`
	Failed              int64  `json:"failed"`
	Running             int64  `json:"running"`
	Paused              int64  `json:"paused"`
	Blocked             int64      `json:"blocked"`
	Drifted             int64      `json:"drifted"`
	Outdated            int64      `json:"outdated"`
	AvgMigrationMs      int64      `json:"avg_migration_duration_ms"`
	LastFleetRunAt      *time.Time `json:"last_fleet_run_at,omitempty"`
	SchemaTargetVersion int    `json:"schema_target_version"`
	WithoutRegistry     int64  `json:"without_registry"`
	CircuitOpen         bool   `json:"circuit_open"`
	CircuitReason       string `json:"circuit_reason,omitempty"`
}

func (s *MigrationFleetService) Summary() (*MigrationSummary, error) {
	raw, err := database.FleetMigrationSummaryQuery()
	if err != nil {
		return nil, errors.New("BD central no conectada")
	}
	return &MigrationSummary{
		Total: raw.Total, Completed: raw.Completed, Pending: raw.Pending,
		Failed: raw.Failed, Running: raw.Running, Paused: raw.Paused,
		Blocked: raw.Blocked, Drifted: raw.Drifted, Outdated: raw.Outdated,
		AvgMigrationMs: raw.AvgMigrationMs, LastFleetRunAt: raw.LastFleetRunAt,
		SchemaTargetVersion: raw.SchemaTargetVersion,
		WithoutRegistry: raw.WithoutRegistry,
		CircuitOpen: raw.CircuitOpen, CircuitReason: raw.CircuitReason,
	}, nil
}

// ResumeFleet cierra circuit breaker global y permite nuevo ciclo cron.
func (s *MigrationFleetService) ResumeFleet(saUserID uint, ip string) error {
	if err := database.ResetFleetCircuitBreaker(); err != nil {
		return err
	}
	logMigrationAudit(0, saUserID, "migration.fleet_resume", "", ip)
	return nil
}

func (s *MigrationFleetService) List(p MigrationListParams) ([]MigrationListItem, int64, error) {
	if database.CentralDB == nil {
		return nil, 0, errors.New("BD central no conectada")
	}
	if p.PerPage <= 0 {
		p.PerPage = 25
	}
	if p.Page <= 0 {
		p.Page = 1
	}
	target := database.TenantSchemaTargetVersion()

	q := database.CentralDB.Table("tenants t").
		Select(`t.id as tenant_id, t.slug as tenant_slug, t.name as company_name,
			COALESCE(tsv.current_version, ?) as current_version,
			COALESCE(tsv.target_version, ?) as target_version,
			COALESCE(tsv.status, 'pending') as status,
			COALESCE(tsv.attempts, 0) as attempts,
			tsv.last_error, tsv.last_migrated_at,
			(COALESCE(tsv.current_version, ?) < COALESCE(tsv.target_version, ?)) as outdated`,
			database.TenantSchemaBaselineVersion, target,
			database.TenantSchemaBaselineVersion, target).
		Joins("LEFT JOIN tenant_schema_versions tsv ON tsv.tenant_id = t.id").
		Where("t.deleted_at IS NULL")

	if p.Status != "" {
		if p.Status == "pending" {
			q = q.Where("COALESCE(tsv.status, 'pending') = ?", database.TenantSchemaStatusPending)
		} else {
			q = q.Where("tsv.status = ?", p.Status)
		}
	}
	if p.Failed {
		q = q.Where("tsv.status = ?", database.TenantSchemaStatusFailed)
	}
	if p.Drifted {
		q = q.Where("tsv.status = ?", database.TenantSchemaStatusDrifted)
	}
	if p.LastMigratedFrom != "" {
		q = q.Where("tsv.last_migrated_at >= ?", p.LastMigratedFrom)
	}
	if p.LastMigratedTo != "" {
		q = q.Where("tsv.last_migrated_at <= ?", p.LastMigratedTo+" 23:59:59")
	}
	if p.Pending {
		q = q.Where("COALESCE(tsv.current_version, ?) < COALESCE(tsv.target_version, ?)",
			database.TenantSchemaBaselineVersion, target)
	}
	if p.Outdated {
		q = q.Where("COALESCE(tsv.current_version, ?) < COALESCE(tsv.target_version, ?)",
			database.TenantSchemaBaselineVersion, target)
	}
	if p.CurrentVersion > 0 {
		q = q.Where("COALESCE(tsv.current_version, ?) = ?", database.TenantSchemaBaselineVersion, p.CurrentVersion)
	}
	if p.TargetVersion > 0 {
		q = q.Where("COALESCE(tsv.target_version, ?) = ?", target, p.TargetVersion)
	}
	if p.TenantSlug != "" {
		q = q.Where("t.slug LIKE ?", "%"+p.TenantSlug+"%")
	}
	if p.TenantName != "" {
		q = q.Where("t.name LIKE ?", "%"+p.TenantName+"%")
	}

	var total int64
	countQ := q.Session(&gorm.Session{})
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []MigrationListItem
	offset := (p.Page - 1) * p.PerPage
	if err := q.Order("outdated DESC, tsv.attempts DESC, t.slug ASC").
		Offset(offset).Limit(p.PerPage).Scan(&items).Error; err != nil {
		return nil, 0, err
	}
	if items == nil {
		items = make([]MigrationListItem, 0)
	}
	for i := range items {
		enrichListItem(&items[i])
	}
	return items, total, nil
}

func (s *MigrationFleetService) Retry(tenantID uint, saUserID uint, ip string) error {
	return s.resetAndMigrate(tenantID, saUserID, ip, "migration.retry")
}

func (s *MigrationFleetService) MigrateOne(tenantID uint, saUserID uint, ip string) error {
	return s.runIncremental(tenantID, saUserID, ip, "migration.migrate_one")
}

func (s *MigrationFleetService) Pause(tenantID uint, saUserID uint, ip string) error {
	return s.setStatus(tenantID, database.TenantSchemaStatusPaused, saUserID, ip, "migration.pause")
}

func (s *MigrationFleetService) Resume(tenantID uint, saUserID uint, ip string) error {
	return s.setStatus(tenantID, database.TenantSchemaStatusPending, saUserID, ip, "migration.resume")
}

func (s *MigrationFleetService) setStatus(tenantID uint, status string, saUserID uint, ip, action string) error {
	tenant, err := s.ensureRegistry(tenantID)
	if err != nil {
		return err
	}
	if err := database.CentralDB.Model(&database.TenantSchemaVersion{}).
		Where("tenant_id = ?", tenantID).
		Updates(map[string]interface{}{
			"status":          status,
			"migration_lock":  nil,
			"lock_expires_at": nil,
			"updated_at":      time.Now(),
		}).Error; err != nil {
		return err
	}
	logMigrationAudit(tenantID, saUserID, action, tenant.Slug, ip)
	return nil
}

func (s *MigrationFleetService) resetAndMigrate(tenantID uint, saUserID uint, ip, action string) error {
	tenant, err := s.ensureRegistry(tenantID)
	if err != nil {
		return err
	}
	_ = database.CentralDB.Model(&database.TenantSchemaVersion{}).
		Where("tenant_id = ?", tenantID).
		Updates(map[string]interface{}{
			"status":          database.TenantSchemaStatusPending,
			"last_error":      nil,
			"next_retry_at":   nil,
			"migration_lock":  nil,
			"lock_expires_at": nil,
			"updated_at":      time.Now(),
		})
	return s.runIncrementalForTenant(tenant, saUserID, ip, action)
}

func (s *MigrationFleetService) runIncremental(tenantID uint, saUserID uint, ip, action string) error {
	tenant, err := s.ensureRegistry(tenantID)
	if err != nil {
		return err
	}
	return s.runIncrementalForTenant(tenant, saUserID, ip, action)
}

func (s *MigrationFleetService) runIncrementalForTenant(tenant *database.Tenant, saUserID uint, ip, action string) error {
	var tsv database.TenantSchemaVersion
	if err := database.CentralDB.Where("tenant_id = ?", tenant.ID).First(&tsv).Error; err != nil {
		return err
	}
	if tsv.Status == database.TenantSchemaStatusPaused {
		return errors.New("tenant en pausa; use resume primero")
	}

	target := database.TenantSchemaTargetVersion()
	if tsv.TargetVersion < target {
		tsv.TargetVersion = target
	}

	res, err := engine.ReconcileTenantSchemaDrift(tenant.ID, tenant.Slug, tenant.DBName, tsv.CurrentVersion, engine.ReconcileOpts{})
	if err != nil {
		return err
	}
	if err := database.CentralDB.Where("tenant_id = ?", tenant.ID).First(&tsv).Error; err != nil {
		return err
	}
	if !res.NeedsMigration(tsv.TargetVersion) {
		if res.ProvenVersion >= tsv.TargetVersion {
			return engine.MarkTenantSchemaCompletedFromHistory(tenant.ID, tenant.DBName, tsv.TargetVersion)
		}
		return nil
	}

	database.RecoverStaleMigrationLocks()
	row := database.PendingTenantRow{
		TenantID: tenant.ID, Slug: tenant.Slug, DBName: tenant.DBName,
		CurrentVersion: res.MigrationFrom, TargetVersion: tsv.TargetVersion,
	}
	err = engine.MigrateFleetOne(row, fmt.Sprintf("sa-%d", saUserID), 15*time.Minute)
	if err != nil {
		_ = database.MarkTenantSchemaFailed(tenant.ID, err)
		var attempts int
		database.CentralDB.Model(&database.TenantSchemaVersion{}).Where("tenant_id = ?", tenant.ID).Select("attempts").Scan(&attempts)
		migrationalert.NotifyMigrationFailure(migrationalert.TenantFailureContext{
			TenantSlug: tenant.Slug, TenantName: tenant.Name,
			Version: tsv.TargetVersion, Attempts: attempts, Error: err.Error(),
		})
		logMigrationAudit(tenant.ID, saUserID, action+".failed", tenant.Slug, ip)
		return err
	}
	database.RemoveTenantFromPool(tenant.DBName)
	logMigrationAudit(tenant.ID, saUserID, action, tenant.Slug, ip)
	return nil
}

func (s *MigrationFleetService) ensureRegistry(tenantID uint) (*database.Tenant, error) {
	var tenant database.Tenant
	if err := database.CentralDB.First(&tenant, tenantID).Error; err != nil {
		return nil, errors.New("tenant no encontrado")
	}
	var tsv database.TenantSchemaVersion
	if err := database.CentralDB.Where("tenant_id = ?", tenantID).First(&tsv).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = database.UpsertTenantSchemaVersion(tenantID, database.TenantSchemaBaselineVersion,
				database.TenantSchemaTargetVersion(), database.TenantSchemaStatusPending)
		} else {
			return nil, err
		}
	}
	return &tenant, nil
}

func logMigrationAudit(tenantID, saUserID uint, action, slug, ip string) {
	if database.CentralDB == nil {
		return
	}
	_ = database.CentralDB.Create(&database.AuditLog{
		TenantID:  tenantID,
		UserID:    saUserID,
		Action:    action,
		Entity:    "tenant_schema_version",
		EntityID:  tenantID,
		Payload:   fmt.Sprintf(`{"slug":%q}`, slug),
		IPAddress: ip,
	}).Error
}

// GuardMigrateAllProduction bloquea migrate-all en producción.
func GuardMigrateAllProduction() error {
	if config.AppConfig != nil && config.AppConfig.IsProd() {
		return errors.New("migrate-all deshabilitado en producción; use migrate-fleet o el dashboard")
	}
	return nil
}
