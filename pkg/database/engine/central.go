package engine

import (
	"fmt"
	"log/slog"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantmigrations"
	"tukifac/pkg/logger"
)

// SyncSchemaTargetVersion alinea el objetivo en BD central con el registry del binario.
func SyncSchemaTargetVersion() {
	database.SetTenantSchemaTargetVersion(tenantmigrations.MaxVersion())
}

// CodeTargetVersion versión objetivo del binario (tenantmigrations registry).
func CodeTargetVersion() int {
	return tenantmigrations.MaxVersion()
}

// InitTenantSchemaVersions delega a database (bootstrap V30).
func InitTenantSchemaVersions() (created, skipped int, err error) {
	return database.InitTenantSchemaVersions()
}

// BumpFleetTargetVersion sube target en central.
func BumpFleetTargetVersion(target int) (int64, error) {
	return database.BumpFleetTargetVersion(target)
}

// MigrateSingleTenant migraciones incrementales desde historial probado hasta target.
func MigrateSingleTenant(slug, dbName string, tenantID uint, current, target int) error {
	db, err := database.OpenTenantDBForMigration(dbName)
	if err != nil {
		return err
	}
	defer database.CloseTenantDB(db)

	res, err := ReconcileTenantSchemaDrift(tenantID, slug, dbName, current, ReconcileOpts{})
	if err != nil {
		return err
	}
	fromV := res.MigrationFrom
	if !res.NeedsMigration(target) {
		if res.ProvenVersion >= target {
			return MarkTenantSchemaCompletedFromHistory(tenantID, dbName, target)
		}
		return nil
	}
	if err := RunTenantSchemaMigrations(db, slug, fromV, target); err != nil {
		return err
	}
	return MarkTenantSchemaCompletedFromHistory(tenantID, dbName, target)
}

// PendingTenantRow alias.
type PendingTenantRow = database.PendingTenantRow

// ListPendingTenants lista pendientes.
func ListPendingTenants(limit int, activeOnly bool) ([]PendingTenantRow, error) {
	return database.ListPendingSchemaTenants(limit, activeOnly)
}

// TryAcquireMigrationLock lease por tenant.
func TryAcquireMigrationLock(tenantID uint, workerID string, lease time.Duration) (bool, error) {
	return database.TryAcquireSchemaMigrationLock(tenantID, workerID, lease)
}

// ReleaseMigrationLock libera lock.
func ReleaseMigrationLock(tenantID uint) error {
	return database.ReleaseSchemaMigrationLock(tenantID)
}

// MarkTenantFailed registra fallo.
func MarkTenantFailed(tenantID uint, err error) error {
	return database.MarkTenantSchemaFailed(tenantID, err)
}

// UpsertTenantSchemaVersion para alta de tenant.
func UpsertTenantSchemaVersion(tenantID uint, current, target int, status string) error {
	return database.UpsertTenantSchemaVersion(tenantID, current, target, status)
}

// MigrateFleetOne procesa un tenant (usado por fleet).
func MigrateFleetOne(row PendingTenantRow, workerID string, lease time.Duration) error {
	ok, err := TryAcquireMigrationLock(row.TenantID, workerID, lease)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("tenant bloqueado por otro worker")
	}
	defer func() { _ = ReleaseMigrationLock(row.TenantID) }()

	res, err := ReconcileTenantSchemaDrift(row.TenantID, row.Slug, row.DBName, row.CurrentVersion, ReconcileOpts{})
	if err != nil {
		return err
	}
	fromV := res.MigrationFrom
	if fromV != row.CurrentVersion {
		row.CurrentVersion = fromV
	}

	logger.L.Info("fleet_tenant_start",
		slog.String("tenant", row.Slug),
		slog.Uint64("tenant_id", uint64(row.TenantID)),
		slog.Int("from", fromV),
		slog.Int("proven", res.ProvenVersion),
		slog.Int("to", row.TargetVersion),
	)
	start := time.Now()
	err = MigrateSingleTenant(row.Slug, row.DBName, row.TenantID, fromV, row.TargetVersion)
	if err != nil {
		return err
	}
	logger.L.Info("fleet_tenant_success",
		slog.String("tenant", row.Slug),
		slog.Uint64("tenant_id", uint64(row.TenantID)),
		slog.Duration("duration", time.Since(start)),
	)
	return nil
}
