package engine

import (
	"fmt"

	"tukifac/pkg/database"
)

// ProvisionTenantDB crea BD vacía, ejecuta migraciones versionadas y seed de datos iniciales.
func ProvisionTenantDB(dbName string, tenantID uint, tenantSlug string, seed database.TenantSeedInput) error {
	if err := database.CreateTenantDB(dbName); err != nil {
		return fmt.Errorf("crear BD: %w", err)
	}
	database.RemoveTenantFromPool(dbName)

	target := database.TenantSchemaTargetVersion()
	if err := database.UpsertTenantSchemaVersion(tenantID, database.TenantSchemaBaselineVersion, target, database.TenantSchemaStatusPending); err != nil {
		_ = database.DropTenantDB(dbName)
		return fmt.Errorf("registrar versión de esquema: %w", err)
	}

	db, err := database.OpenTenantDBForMigration(dbName)
	if err != nil {
		_ = database.DropTenantDB(dbName)
		return fmt.Errorf("conectar BD tenant: %w", err)
	}
	defer database.CloseTenantDB(db)

	if err := RunTenantSchemaMigrations(db, tenantSlug, database.TenantSchemaBaselineVersion, target); err != nil {
		_ = database.DropTenantDB(dbName)
		_ = database.UpsertTenantSchemaVersion(tenantID, database.TenantSchemaBaselineVersion, target, database.TenantSchemaStatusFailed)
		return fmt.Errorf("migrar esquema: %w", err)
	}
	if err := MarkTenantSchemaCompletedFromHistory(tenantID, dbName, target); err != nil {
		_ = database.DropTenantDB(dbName)
		return fmt.Errorf("marcar esquema completado: %w", err)
	}

	if err := database.SeedUbigeoRegionesProvincias(db); err != nil {
		_ = database.DropTenantDB(dbName)
		return fmt.Errorf("seed ubigeo regiones/provincias: %w", err)
	}
	if err := database.SeedUbigeoDistritos(db, database.UbigeoDistritosCSVPath()); err != nil {
		_ = database.DropTenantDB(dbName)
		return fmt.Errorf("seed ubigeo distritos: %w", err)
	}
	if err := database.SeedPaymentMethodsIfEmpty(db); err != nil {
		_ = database.DropTenantDB(dbName)
		return fmt.Errorf("seed payment methods: %w", err)
	}
	if err := database.ProvisionTenantSeed(db, seed); err != nil {
		_ = database.DropTenantDB(dbName)
		return fmt.Errorf("seed tenant: %w", err)
	}
	database.RemoveTenantFromPool(dbName)
	return nil
}
