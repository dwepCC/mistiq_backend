package engine

import (
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"
)

// ReconcileOpts opciones de reconciliación de drift.
type ReconcileOpts struct {
	DryRun bool
}

// ReconcileResult estado tras analizar/reparar drift.
type ReconcileResult struct {
	MigrationFrom   int
	ProvenVersion   int
	DeclaredVersion int
	DriftDetected   bool
	Issues          []string
	InvalidatedFrom int
	RowsInvalidated int64
}

// NeedsMigration indica si faltan migraciones por ejecutar hasta el target central.
func (r ReconcileResult) NeedsMigration(target int) bool {
	if r.MigrationFrom < target {
		return true
	}
	return r.DriftDetected && r.ProvenVersion < target
}

// ReconcileTenantSchemaDrift detecta drift, repara historial falso-positivo y sincroniza central.
func ReconcileTenantSchemaDrift(tenantID uint, slug, dbName string, declaredCurrent int, opts ReconcileOpts) (ReconcileResult, error) {
	result := ReconcileResult{DeclaredVersion: declaredCurrent}
	if database.CentralDB == nil {
		return result, fmt.Errorf("BD central no conectada")
	}
	db, err := database.OpenTenantDBForMigration(dbName)
	if err != nil {
		return result, err
	}
	defer database.CloseTenantDB(db)

	if minV, ok := detectPhysicalDriftMinVersion(db); ok {
		result.DriftDetected = true
		result.InvalidatedFrom = minV
		result.Issues = append(result.Issues, fmt.Sprintf("drift físico desde V%d: historial success sin objeto de esquema", minV))
		if !opts.DryRun {
			n, invErr := invalidateSchemaHistoryFromVersion(db, minV)
			if invErr != nil {
				return result, invErr
			}
			result.RowsInvalidated = n
		}
	}

	proven, err := DeriveCurrentVersionFromHistory(db)
	if err != nil {
		return result, err
	}
	result.ProvenVersion = proven

	target := database.TenantSchemaTargetVersion()
	checkUpTo := declaredCurrent
	if target > checkUpTo {
		checkUpTo = target
	}
	report := DetectSchemaDrift(db, checkUpTo)
	if declaredCurrent > proven {
		report.Issues = append(report.Issues,
			fmt.Sprintf("current_version %d > historial probado V%d", declaredCurrent, proven))
	}
	if report.HasDrift() {
		result.DriftDetected = true
		result.Issues = append(result.Issues, report.Issues...)
	}

	result.MigrationFrom = proven
	if lowest, ok := LowestMissingVersion(db, checkUpTo); ok && lowest > 0 {
		from := lowest - 1
		if from < result.MigrationFrom {
			result.MigrationFrom = from
		}
	}

	if !opts.DryRun {
		status := database.TenantSchemaStatusCompleted
		if result.DriftDetected || proven < target {
			status = database.TenantSchemaStatusDrifted
		}
		if proven >= target && !result.DriftDetected {
			status = database.TenantSchemaStatusCompleted
		}
		msg := ""
		if len(result.Issues) > 0 {
			msg = strings.Join(result.Issues, "; ")
		}
		now := time.Now()
		updates := map[string]interface{}{
			"current_version": proven,
			"target_version":  target,
			"status":          status,
			"updated_at":      now,
		}
		if msg != "" {
			updates["last_error"] = msg
		} else {
			updates["last_error"] = nil
		}
		_ = database.CentralDB.Model(&database.TenantSchemaVersion{}).
			Where("tenant_id = ?", tenantID).
			Updates(updates).Error
	}

	return result, nil
}

// ScanSchemaDriftBatch revisa tenants completed y sincroniza drift con el historial.
func ScanSchemaDriftBatch(limit int) (int, error) {
	if database.CentralDB == nil {
		return 0, fmt.Errorf("BD central no conectada")
	}
	if limit <= 0 {
		limit = 25
	}
	type row struct {
		TenantID       uint
		Slug           string
		DBName         string
		CurrentVersion int
	}
	var rows []row
	err := database.CentralDB.Table("tenant_schema_versions AS tsv").
		Select("tsv.tenant_id, t.slug, t.db_name, tsv.current_version").
		Joins("INNER JOIN tenants AS t ON t.id = tsv.tenant_id AND t.deleted_at IS NULL").
		Where("tsv.status = ?", database.TenantSchemaStatusCompleted).
		Order("tsv.updated_at ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return 0, err
	}
	marked := 0
	for _, r := range rows {
		res, err := ReconcileTenantSchemaDrift(r.TenantID, r.Slug, r.DBName, r.CurrentVersion, ReconcileOpts{})
		if err != nil {
			continue
		}
		if res.DriftDetected || res.ProvenVersion < r.CurrentVersion {
			marked++
		}
	}
	return marked, nil
}
