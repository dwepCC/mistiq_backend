package engine

import (
	"fmt"

	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantmigrations"

	"gorm.io/gorm"
)

// DeriveCurrentVersionFromHistory devuelve la versión más alta del registry cuya cadena
// de migraciones schema está completa en tenant_migration_history (success=true).
// Es la fuente de verdad para current_version.
func DeriveCurrentVersionFromHistory(db *gorm.DB) (int, error) {
	if db == nil {
		return database.TenantSchemaBaselineVersion, nil
	}
	if !db.Migrator().HasTable(&database.TenantMigrationHistory{}) {
		return database.TenantSchemaBaselineVersion, nil
	}
	proven := database.TenantSchemaBaselineVersion
	for _, mig := range tenantmigrations.TenantMigrations {
		v := mig.Version()
		applied, err := isHistoryApplied(db, v, database.MigrationHistoryTypeSchema)
		if err != nil {
			return proven, err
		}
		if !applied {
			break
		}
		proven = v
	}
	return proven, nil
}

// MarkTenantSchemaCompletedFromHistory sincroniza central con el historial real del tenant.
// minExpected es la versión mínima que debe estar probada (p. ej. target tras migrar).
func MarkTenantSchemaCompletedFromHistory(tenantID uint, dbName string, minExpected int) error {
	db, err := database.OpenTenantDBForMigration(dbName)
	if err != nil {
		return err
	}
	defer database.CloseTenantDB(db)

	proven, err := DeriveCurrentVersionFromHistory(db)
	if err != nil {
		return err
	}
	if minExpected > 0 && proven < minExpected {
		return fmt.Errorf("historial incompleto: versión probada V%d < esperada V%d", proven, minExpected)
	}
	target := database.TenantSchemaTargetVersion()
	status := database.TenantSchemaStatusCompleted
	if proven < target {
		status = database.TenantSchemaStatusPending
	}
	return database.SyncTenantSchemaCurrentVersion(tenantID, proven, status)
}

// invalidateSchemaHistoryFromVersion marca como fallidos los registros exitosos desde fromVersion.
func invalidateSchemaHistoryFromVersion(db *gorm.DB, fromVersion int) (int64, error) {
	if fromVersion <= 0 || db == nil {
		return 0, nil
	}
	if !db.Migrator().HasTable(&database.TenantMigrationHistory{}) {
		return 0, nil
	}
	errMsg := "invalidado: drift físico detectado"
	res := db.Model(&database.TenantMigrationHistory{}).
		Where("type = ? AND version >= ? AND success = ?", database.MigrationHistoryTypeSchema, fromVersion, true).
		Updates(map[string]interface{}{
			"success": false,
			"error":   errMsg,
		})
	return res.RowsAffected, res.Error
}
