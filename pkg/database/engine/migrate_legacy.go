package engine

import (
	"fmt"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
)

// MigrateSummary resultado de migrar múltiples tenants.
type MigrateSummary struct {
	Success []string
	Failed  []database.TenantMigrateFailure
}

// MigrateTenantSchemaByDBName migraciones versionadas para una BD tenant.
func MigrateTenantSchemaByDBName(dbName string) error {
	if database.CentralDB == nil {
		return fmt.Errorf("BD central no conectada")
	}
	var tenant database.Tenant
	if err := database.CentralDB.Where("db_name = ?", dbName).First(&tenant).Error; err != nil {
		return fmt.Errorf("tenant no encontrado para %s: %w", dbName, err)
	}
	return MigrateTenantIncremental(tenant.ID, tenant.Slug, dbName)
}

// MigrateTenantBySlug migraciones versionadas por slug.
func MigrateTenantBySlug(slug string) error {
	if database.CentralDB == nil {
		return fmt.Errorf("BD central no conectada")
	}
	var tenant database.Tenant
	if err := database.CentralDB.Where("slug = ?", slug).First(&tenant).Error; err != nil {
		return fmt.Errorf("tenant no encontrado: %w", err)
	}
	return MigrateTenantIncremental(tenant.ID, tenant.Slug, tenant.DBName)
}

// MigrateTenantsBatch ejecuta migraciones versionadas por tenant (emergencia / CLI).
func MigrateTenantsBatch(activeOnly bool, onProgress func(slug string, err error)) MigrateSummary {
	cfg := config.AppConfig
	batchSize := cfg.MigrationBatchSize
	if batchSize <= 0 {
		batchSize = 50
	}
	pause := cfg.MigrationBatchPause

	tenants, err := database.ListTenantsForMigration(activeOnly)
	summary := MigrateSummary{}
	if err != nil {
		summary.Failed = append(summary.Failed, database.TenantMigrateFailure{Slug: "(list)", Err: err})
		if onProgress != nil {
			onProgress("(list)", err)
		}
		return summary
	}

	for i, t := range tenants {
		migErr := MigrateTenantIncremental(t.ID, t.Slug, t.DBName)
		if onProgress != nil {
			onProgress(t.Slug, migErr)
		}
		if migErr != nil {
			summary.Failed = append(summary.Failed, database.TenantMigrateFailure{Slug: t.Slug, DBName: t.DBName, Err: migErr})
		} else {
			summary.Success = append(summary.Success, t.Slug)
		}
		if pause > 0 && batchSize > 0 && (i+1)%batchSize == 0 && i+1 < len(tenants) {
			time.Sleep(pause)
		}
	}
	return summary
}
