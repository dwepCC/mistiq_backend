package database

import "gorm.io/gorm"

// OpenTenantDBForMigration abre conexión tenant sin pool (CLI / workers).
func OpenTenantDBForMigration(dbName string) (*gorm.DB, error) {
	// Límites de espera de locks SOLO para la conexión de migración: por defecto un ALTER espera un año el lock de metadatos y, mientras
	// espera, bloquea detrás de sí todas las consultas de esa tabla. Con 20 s la migración falla rápido (se reintenta sola) en vez de
	// dejar parado al tenant.
	return openTenantDBWithDSN(dbName, tenantDSN(dbName)+"&lock_wait_timeout=20&innodb_lock_wait_timeout=20")
}

// CloseTenantDB cierra conexión abierta para migración.
func CloseTenantDB(db *gorm.DB) {
	closeDB(db)
}
