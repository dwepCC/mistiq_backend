package database

import (
	"errors"
	"sync"
)

var (
	ensureCentralFleetSchemaOnce sync.Once
	ensureCentralFleetSchemaErr  error
)

// EnsureCentralFleetSchema aplica columnas/tablas del fleet en BD central (idempotente).
// Se invoca al arrancar el API y antes de operaciones de tenant_schema_versions.
func EnsureCentralFleetSchema() error {
	ensureCentralFleetSchemaOnce.Do(func() {
		if CentralDB == nil {
			ensureCentralFleetSchemaErr = errors.New("BD central no conectada")
			return
		}
		if err := CentralDB.AutoMigrate(&TenantSchemaVersion{}, &FleetMigrationState{}, &MigrationBatchJob{}); err != nil {
			ensureCentralFleetSchemaErr = err
			return
		}
		ensureCentralFleetSchemaErr = EnsureFleetMigrationState()
	})
	return ensureCentralFleetSchemaErr
}

// HasTenantSchemaNextRetryColumn indica si la columna de backoff ya existe.
func HasTenantSchemaNextRetryColumn() bool {
	if CentralDB == nil {
		return false
	}
	return CentralDB.Migrator().HasColumn(&TenantSchemaVersion{}, "NextRetryAt")
}
