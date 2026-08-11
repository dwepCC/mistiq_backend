package engine

import (
	"fmt"

	"tukifac/pkg/database"
)

// MigrateTenantIncremental ejecuta migraciones versionadas pendientes para un tenant.
func MigrateTenantIncremental(tenantID uint, slug, dbName string) error {
	if database.CentralDB == nil {
		return fmt.Errorf("BD central no conectada")
	}
	var tsv database.TenantSchemaVersion
	if err := database.CentralDB.Where("tenant_id = ?", tenantID).First(&tsv).Error; err != nil {
		return fmt.Errorf("tenant_schema_versions: %w", err)
	}
	target := database.TenantSchemaTargetVersion()
	if tsv.TargetVersion < target {
		tsv.TargetVersion = target
	}

	res, err := ReconcileTenantSchemaDrift(tenantID, slug, dbName, tsv.CurrentVersion, ReconcileOpts{})
	if err != nil {
		return err
	}
	if !res.NeedsMigration(tsv.TargetVersion) {
		if res.ProvenVersion >= tsv.TargetVersion {
			return MarkTenantSchemaCompletedFromHistory(tenantID, dbName, tsv.TargetVersion)
		}
		return nil
	}

	db, err := database.OpenTenantDBForMigration(dbName)
	if err != nil {
		return err
	}
	defer database.CloseTenantDB(db)
	if err := RunTenantSchemaMigrations(db, slug, res.MigrationFrom, tsv.TargetVersion); err != nil {
		_ = database.MarkTenantSchemaFailed(tenantID, err)
		return err
	}
	return MarkTenantSchemaCompletedFromHistory(tenantID, dbName, tsv.TargetVersion)
}
