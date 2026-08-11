package engine

import (
	"fmt"
	"strings"

	"tukifac/pkg/database"
)

// RepairOptions configuración del comando repair-tenant-migrations.
type RepairOptions struct {
	Slug          string
	Limit         int
	DryRun        bool
	ReconcileOnly bool // solo reconciliar drift, sin ejecutar migraciones
}

// RepairTenantOutcome resultado por tenant.
type RepairTenantOutcome struct {
	TenantID        uint
	Slug            string
	DeclaredBefore  int
	ProvenBefore    int
	ProvenAfter     int
	InvalidatedFrom int
	RowsInvalidated int64
	DriftDetected   bool
	Issues          []string
	Migrated        bool
	MigrateError    string
}

// RepairTenantMigrations reconcilia drift y opcionalmente ejecuta migraciones pendientes.
func RepairTenantMigrations(opts RepairOptions) ([]RepairTenantOutcome, error) {
	if database.CentralDB == nil {
		return nil, fmt.Errorf("BD central no conectada")
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	tenants, err := listTenantsForRepair(opts.Slug, opts.Limit)
	if err != nil {
		return nil, err
	}

	outcomes := make([]RepairTenantOutcome, 0, len(tenants))
	for _, t := range tenants {
		outcome, err := repairOneTenant(t, opts)
		if err != nil {
			outcome.MigrateError = err.Error()
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

type repairTenantRow struct {
	TenantID       uint
	Slug           string
	DBName         string
	CurrentVersion int
	TargetVersion  int
}

func listTenantsForRepair(slug string, limit int) ([]repairTenantRow, error) {
	q := database.CentralDB.Table("tenants AS t").
		Select(`t.id as tenant_id, t.slug, t.db_name,
			COALESCE(tsv.current_version, ?) as current_version,
			COALESCE(tsv.target_version, ?) as target_version`,
			database.TenantSchemaBaselineVersion, database.TenantSchemaTargetVersion()).
		Joins("LEFT JOIN tenant_schema_versions tsv ON tsv.tenant_id = t.id").
		Where("t.deleted_at IS NULL")
	if slug != "" {
		q = q.Where("t.slug = ?", slug)
	}
	q = q.Order("t.slug ASC").Limit(limit)

	var rows []repairTenantRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func repairOneTenant(t repairTenantRow, opts RepairOptions) (RepairTenantOutcome, error) {
	outcome := RepairTenantOutcome{
		TenantID:       t.TenantID,
		Slug:           t.Slug,
		DeclaredBefore: t.CurrentVersion,
	}

	db, err := database.OpenTenantDBForMigration(t.DBName)
	if err != nil {
		return outcome, err
	}
	provenBefore, err := DeriveCurrentVersionFromHistory(db)
	database.CloseTenantDB(db)
	if err != nil {
		return outcome, err
	}
	outcome.ProvenBefore = provenBefore

	reconcileOpts := ReconcileOpts{DryRun: opts.DryRun}
	res, err := ReconcileTenantSchemaDrift(t.TenantID, t.Slug, t.DBName, t.CurrentVersion, reconcileOpts)
	if err != nil {
		return outcome, err
	}
	outcome.ProvenAfter = res.ProvenVersion
	outcome.InvalidatedFrom = res.InvalidatedFrom
	outcome.RowsInvalidated = res.RowsInvalidated
	outcome.DriftDetected = res.DriftDetected
	outcome.Issues = res.Issues

	if opts.DryRun || opts.ReconcileOnly {
		return outcome, nil
	}
	if !res.NeedsMigration(t.TargetVersion) {
		if res.ProvenVersion >= t.TargetVersion {
			if err := MarkTenantSchemaCompletedFromHistory(t.TenantID, t.DBName, t.TargetVersion); err != nil {
				return outcome, err
			}
		}
		return outcome, nil
	}

	if err := MigrateTenantIncremental(t.TenantID, t.Slug, t.DBName); err != nil {
		return outcome, err
	}
	outcome.Migrated = true
	return outcome, nil
}

// FormatRepairOutcome línea legible para CLI.
func FormatRepairOutcome(o RepairTenantOutcome) string {
	parts := []string{o.Slug}
	if o.DriftDetected {
		parts = append(parts, "DRIFT")
	}
	parts = append(parts, fmt.Sprintf("declared=V%d", o.DeclaredBefore))
	parts = append(parts, fmt.Sprintf("proven=V%d→V%d", o.ProvenBefore, o.ProvenAfter))
	if o.InvalidatedFrom > 0 {
		parts = append(parts, fmt.Sprintf("invalidated_from=V%d(%d rows)", o.InvalidatedFrom, o.RowsInvalidated))
	}
	if len(o.Issues) > 0 {
		parts = append(parts, strings.Join(o.Issues, "; "))
	}
	if o.Migrated {
		parts = append(parts, "migrated=ok")
	}
	if o.MigrateError != "" {
		parts = append(parts, "error="+o.MigrateError)
	}
	return strings.Join(parts, " | ")
}
