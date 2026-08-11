package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/database/engine"
	"tukifac/pkg/database/tenantmigrations"
)

// MigrationHistoryItem fila de tenant_migration_history.
type MigrationHistoryItem struct {
	ID         uint      `json:"id"`
	Version    int       `json:"version"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Success    bool      `json:"success"`
	AppliedAt  time.Time `json:"applied_at"`
	DurationMs int64     `json:"duration_ms"`
	Error      *string   `json:"error,omitempty"`
	Checksum   *string   `json:"checksum,omitempty"`
}

// TenantDriftReport resultado de escaneo de drift.
type TenantDriftReport struct {
	TenantID        uint     `json:"tenant_id"`
	TenantSlug      string   `json:"tenant_slug"`
	DeclaredVersion int      `json:"declared_version"`
	ProvenVersion   int      `json:"proven_version"`
	DriftDetected   bool     `json:"drift_detected"`
	Issues          []string `json:"issues"`
}

// DriftScanParams parámetros de escaneo.
type DriftScanParams struct {
	TenantID uint
	Limit    int
	DryRun   bool
}

// RepairParams reparación de un tenant.
type RepairParams struct {
	TenantID      uint
	DryRun        bool
	ReconcileOnly bool
}

// RepairResult resultado de reparación.
type RepairResult struct {
	TenantID        uint     `json:"tenant_id"`
	TenantSlug      string   `json:"tenant_slug"`
	DriftDetected   bool     `json:"drift_detected"`
	DeclaredBefore  int      `json:"declared_before"`
	ProvenBefore    int      `json:"proven_before"`
	ProvenAfter     int      `json:"proven_after"`
	InvalidatedFrom int      `json:"invalidated_from,omitempty"`
	RowsInvalidated int64    `json:"rows_invalidated,omitempty"`
	Issues          []string `json:"issues,omitempty"`
	Migrated        bool     `json:"migrated"`
	Error           string   `json:"error,omitempty"`
}

func enrichListItem(item *MigrationListItem) {
	item.MigrationsApplied = tenantmigrations.CountRegisteredUpTo(item.CurrentVersion)
	item.MigrationsPending = tenantmigrations.CountPendingBetween(item.CurrentVersion, item.TargetVersion)
}

// DriftScan analiza inconsistencias sin migrar (dry-run por defecto en batch).
func (s *MigrationFleetService) DriftScan(p DriftScanParams) ([]TenantDriftReport, error) {
	if database.CentralDB == nil {
		return nil, errors.New("BD central no conectada")
	}
	if p.TenantID > 0 {
		report, err := s.driftReportForTenant(p.TenantID, p.DryRun)
		if err != nil {
			return nil, err
		}
		return []TenantDriftReport{*report}, nil
	}
	if p.Limit <= 0 {
		p.Limit = 50
	}
	tenants, err := listTenantsForOps("", p.Limit, "")
	if err != nil {
		return nil, err
	}
	out := make([]TenantDriftReport, 0, len(tenants))
	for _, t := range tenants {
		report, err := s.driftReportForTenantRow(t, p.DryRun)
		if err != nil {
			continue
		}
		if report.DriftDetected {
			out = append(out, *report)
		}
	}
	return out, nil
}

func (s *MigrationFleetService) driftReportForTenant(tenantID uint, dryRun bool) (*TenantDriftReport, error) {
	tenant, err := s.ensureRegistry(tenantID)
	if err != nil {
		return nil, err
	}
	var tsv database.TenantSchemaVersion
	_ = database.CentralDB.Where("tenant_id = ?", tenantID).First(&tsv).Error
	return s.driftReportForTenantRow(tenantOpsRow{
		TenantID: tenant.ID, Slug: tenant.Slug, DBName: tenant.DBName, CurrentVersion: tsv.CurrentVersion,
	}, dryRun)
}

type tenantOpsRow struct {
	TenantID       uint
	Slug           string
	DBName         string
	CurrentVersion int
}

func (s *MigrationFleetService) driftReportForTenantRow(t tenantOpsRow, dryRun bool) (*TenantDriftReport, error) {
	db, err := database.OpenTenantDBForMigration(t.DBName)
	if err != nil {
		return nil, err
	}
	defer database.CloseTenantDB(db)

	proven, _ := engine.DeriveCurrentVersionFromHistory(db)
	report := engine.DetectSchemaDrift(db, maxInt(t.CurrentVersion, database.TenantSchemaTargetVersion()))
	issues := append([]string{}, report.Issues...)
	if t.CurrentVersion > proven {
		issues = append(issues, fmt.Sprintf("current_version %d > historial probado V%d", t.CurrentVersion, proven))
	}

	res, err := engine.ReconcileTenantSchemaDrift(t.TenantID, t.Slug, t.DBName, t.CurrentVersion, engine.ReconcileOpts{DryRun: dryRun})
	if err != nil {
		return nil, err
	}
	if len(res.Issues) > 0 {
		issues = append(issues, res.Issues...)
	}
	drift := len(issues) > 0 || res.DriftDetected
	return &TenantDriftReport{
		TenantID: t.TenantID, TenantSlug: t.Slug,
		DeclaredVersion: t.CurrentVersion, ProvenVersion: res.ProvenVersion,
		DriftDetected: drift, Issues: uniqueStrings(issues),
	}, nil
}

// RepairTenant ejecuta repair-tenant-migrations para un tenant.
func (s *MigrationFleetService) RepairTenant(p RepairParams, saUserID uint, ip string) (*RepairResult, error) {
	tenant, err := s.ensureRegistry(p.TenantID)
	if err != nil {
		return nil, err
	}
	var tsv database.TenantSchemaVersion
	_ = database.CentralDB.Where("tenant_id = ?", p.TenantID).First(&tsv).Error

	db, err := database.OpenTenantDBForMigration(tenant.DBName)
	if err != nil {
		return nil, err
	}
	provenBefore, _ := engine.DeriveCurrentVersionFromHistory(db)
	database.CloseTenantDB(db)

	outcomes, err := engine.RepairTenantMigrations(engine.RepairOptions{
		Slug:          tenant.Slug,
		Limit:         1,
		DryRun:        p.DryRun,
		ReconcileOnly: p.ReconcileOnly,
	})
	if err != nil {
		return nil, err
	}
	result := &RepairResult{
		TenantID: p.TenantID, TenantSlug: tenant.Slug,
		DeclaredBefore: tsv.CurrentVersion, ProvenBefore: provenBefore,
	}
	if len(outcomes) > 0 {
		o := outcomes[0]
		result.DriftDetected = o.DriftDetected
		result.ProvenAfter = o.ProvenAfter
		result.InvalidatedFrom = o.InvalidatedFrom
		result.RowsInvalidated = o.RowsInvalidated
		result.Issues = o.Issues
		result.Migrated = o.Migrated
		result.Error = o.MigrateError
	}
	if !p.DryRun {
		action := "migration.repair"
		if p.ReconcileOnly {
			action = "migration.repair_reconcile"
		}
		logMigrationAudit(p.TenantID, saUserID, action, tenant.Slug, ip)
	}
	return result, nil
}

// TenantMigrationHistory lista historial de migraciones de un tenant.
func (s *MigrationFleetService) TenantMigrationHistory(tenantID uint, limit int) ([]MigrationHistoryItem, error) {
	tenant, err := s.ensureRegistry(tenantID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	db, err := database.OpenTenantDBForMigration(tenant.DBName)
	if err != nil {
		return nil, err
	}
	defer database.CloseTenantDB(db)
	if !db.Migrator().HasTable(&database.TenantMigrationHistory{}) {
		return []MigrationHistoryItem{}, nil
	}
	var rows []database.TenantMigrationHistory
	if err := db.Order("version DESC, applied_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]MigrationHistoryItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, MigrationHistoryItem{
			ID: r.ID, Version: r.Version, Name: r.Name, Type: r.Type,
			Success: r.Success, AppliedAt: r.AppliedAt, DurationMs: r.DurationMs,
			Error: r.Error, Checksum: r.Checksum,
		})
	}
	return out, nil
}

// ListJobs trabajos recientes del panel.
func (s *MigrationFleetService) ListJobs(limit int) ([]database.MigrationBatchJob, error) {
	if database.CentralDB == nil {
		return nil, errors.New("BD central no conectada")
	}
	if limit <= 0 {
		limit = 20
	}
	var jobs []database.MigrationBatchJob
	err := database.CentralDB.Order("id DESC").Limit(limit).Find(&jobs).Error
	return jobs, err
}

// GetJob detalle de un trabajo.
func (s *MigrationFleetService) GetJob(jobID uint) (*database.MigrationBatchJob, error) {
	if database.CentralDB == nil {
		return nil, errors.New("BD central no conectada")
	}
	var job database.MigrationBatchJob
	if err := database.CentralDB.First(&job, jobID).Error; err != nil {
		return nil, errors.New("trabajo no encontrado")
	}
	return &job, nil
}

func listTenantsForOps(slug string, limit int, status string) ([]tenantOpsRow, error) {
	q := database.CentralDB.Table("tenants t").
		Select("t.id as tenant_id, t.slug, t.db_name, COALESCE(tsv.current_version, 0) as current_version").
		Joins("LEFT JOIN tenant_schema_versions tsv ON tsv.tenant_id = t.id").
		Where("t.deleted_at IS NULL")
	if slug != "" {
		q = q.Where("t.slug = ?", slug)
	}
	if status != "" {
		q = q.Where("tsv.status = ?", status)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []tenantOpsRow
	return rows, q.Order("t.slug ASC").Scan(&rows).Error
}

func tenantIDsToRows(ids []uint) ([]tenantOpsRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []tenantOpsRow
	err := database.CentralDB.Table("tenants t").
		Select("t.id as tenant_id, t.slug, t.db_name, COALESCE(tsv.current_version, 0) as current_version").
		Joins("LEFT JOIN tenant_schema_versions tsv ON tsv.tenant_id = t.id").
		Where("t.id IN ? AND t.deleted_at IS NULL", ids).
		Scan(&rows).Error
	return rows, err
}

func encodeJobResults(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
