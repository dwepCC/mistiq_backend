package database

import (
	"fmt"
)

// MigrateSummary resultado de migrar múltiples tenants (alias para engine).
type MigrateSummary struct {
	Success []string
	Failed  []TenantMigrateFailure
}

type TenantMigrateFailure struct {
	Slug   string
	DBName string
	Err    error
}

// RunCentralMigration crea (si falta) y migra la BD central + seeds idempotentes.
func RunCentralMigration() error {
	if err := EnsureCentralDB(); err != nil {
		return fmt.Errorf("ensure central database: %w", err)
	}
	if CentralDB == nil {
		if err := ConnectCentral(); err != nil {
			return err
		}
	}
	if err := MigrateCentral(); err != nil {
		return fmt.Errorf("migrate central: %w", err)
	}
	if err := MigrateModuleKeySaasToMemberships(); err != nil {
		return fmt.Errorf("migrate module key saas→memberships: %w", err)
	}
	if err := SeedCentral(); err != nil {
		return fmt.Errorf("seed central: %w", err)
	}
	if err := EnsureCentralFleetSchema(); err != nil {
		return fmt.Errorf("central fleet schema: %w", err)
	}
	return nil
}

// ListTenantsForMigration devuelve tenants a migrar en lote.
func ListTenantsForMigration(activeOnly bool) ([]Tenant, error) {
	if CentralDB == nil {
		return nil, fmt.Errorf("BD central no conectada")
	}
	var tenants []Tenant
	q := CentralDB.Order("slug ASC")
	if activeOnly {
		q = q.Where("status = ?", "active")
	}
	if err := q.Find(&tenants).Error; err != nil {
		return nil, err
	}
	return tenants, nil
}

// MigrateProgress callback opcional por tenant (slug, err).
type MigrateProgress func(slug string, err error)
