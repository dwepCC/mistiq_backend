package database

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// InitTenantSchemaVersions registra baseline V30 para todos los tenants (idempotente).
func InitTenantSchemaVersions() (created, skipped int, err error) {
	if CentralDB == nil {
		return 0, 0, errors.New("BD central no conectada")
	}
	if err := EnsureCentralFleetSchema(); err != nil {
		return 0, 0, err
	}
	var tenants []Tenant
	if err := CentralDB.Find(&tenants).Error; err != nil {
		return 0, 0, err
	}
	target := TenantSchemaTargetVersion()
	for _, t := range tenants {
		var existing TenantSchemaVersion
		err := CentralDB.Where("tenant_id = ?", t.ID).First(&existing).Error
		if err == nil {
			skipped++
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return created, skipped, err
		}
		row := TenantSchemaVersion{
			TenantID:       t.ID,
			CurrentVersion: TenantSchemaBaselineVersion,
			TargetVersion:  target,
			Status:         TenantSchemaStatusPending,
		}
		if err := CentralDB.Create(&row).Error; err != nil {
			return created, skipped, err
		}
		created++
	}
	return created, skipped, nil
}

// BumpFleetTargetVersion sube target_version pendiente de migrar.
func BumpFleetTargetVersion(target int) (int64, error) {
	if CentralDB == nil {
		return 0, errors.New("BD central no conectada")
	}
	res := CentralDB.Model(&TenantSchemaVersion{}).
		Where("target_version < ?", target).
		Updates(map[string]interface{}{
			"target_version": target,
			"status":         TenantSchemaStatusPending,
		})
	return res.RowsAffected, res.Error
}

// UpsertTenantSchemaVersion crea o actualiza registro central.
func UpsertTenantSchemaVersion(tenantID uint, current, target int, status string) error {
	if CentralDB == nil {
		return nil
	}
	if err := EnsureCentralFleetSchema(); err != nil {
		return err
	}
	now := time.Now()
	row := TenantSchemaVersion{
		TenantID:       tenantID,
		CurrentVersion: current,
		TargetVersion:  target,
		Status:         status,
		LastMigratedAt: &now,
	}
	return CentralDB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"current_version":  current,
			"target_version":   target,
			"status":           status,
			"last_migrated_at": now,
			"updated_at":       now,
		}),
	}).Create(&row).Error
}

// SyncTenantSchemaCurrentVersion actualiza current_version desde el historial probado.
func SyncTenantSchemaCurrentVersion(tenantID uint, provenVersion int, status string) error {
	if CentralDB == nil {
		return errors.New("BD central no conectada")
	}
	now := time.Now()
	updates := map[string]interface{}{
		"current_version": provenVersion,
		"status":          status,
		"updated_at":      now,
	}
	if status == TenantSchemaStatusCompleted {
		updates["last_migrated_at"] = now
		updates["migration_lock"] = nil
		updates["lock_expires_at"] = nil
		updates["last_error"] = nil
		updates["next_retry_at"] = nil
		updates["attempts"] = 0
	}
	return CentralDB.Model(&TenantSchemaVersion{}).
		Where("tenant_id = ?", tenantID).
		Updates(updates).Error
}

// MarkTenantSchemaCompleted tras migración OK (preferir engine.MarkTenantSchemaCompletedFromHistory).
func MarkTenantSchemaCompleted(tenantID uint, version int) error {
	now := time.Now()
	return CentralDB.Model(&TenantSchemaVersion{}).
		Where("tenant_id = ?", tenantID).
		Updates(map[string]interface{}{
			"current_version":  version,
			"status":           TenantSchemaStatusCompleted,
			"last_migrated_at": now,
			"migration_lock":   nil,
			"lock_expires_at":  nil,
			"last_error":       nil,
			"next_retry_at":    nil,
			"attempts":         0,
			"updated_at":       now,
		}).Error
}

// MarkTenantSchemaFailed aísla fallo de tenant y programa backoff exponencial.
func MarkTenantSchemaFailed(tenantID uint, err error) error {
	msg := err.Error()
	now := time.Now()
	var attempts int
	_ = CentralDB.Model(&TenantSchemaVersion{}).Where("tenant_id = ?", tenantID).
		Select("attempts").Scan(&attempts)
	attempts++
	nextRetry := now.Add(FleetBackoffDelay(attempts))
	return CentralDB.Model(&TenantSchemaVersion{}).
		Where("tenant_id = ?", tenantID).
		Updates(map[string]interface{}{
			"status":          TenantSchemaStatusFailed,
			"last_error":      msg,
			"attempts":        attempts,
			"next_retry_at":   nextRetry,
			"migration_lock":  nil,
			"lock_expires_at": nil,
			"updated_at":      now,
		}).Error
}

// PendingTenantRow tenant pendiente de migrar.
type PendingTenantRow struct {
	TenantID       uint
	Slug           string
	DBName         string
	CurrentVersion int
	TargetVersion  int
}

// ListPendingSchemaTenants tenants con current < target.
func ListPendingSchemaTenants(limit int, activeOnly bool) ([]PendingTenantRow, error) {
	if CentralDB == nil {
		return nil, errors.New("BD central no conectada")
	}
	now := time.Now()
	open, _, _ := IsFleetCircuitOpen()
	if open {
		return nil, nil
	}

	q := CentralDB.Table("tenant_schema_versions AS tsv").
		Select("tsv.tenant_id, t.slug, t.db_name, tsv.current_version, tsv.target_version").
		Joins("INNER JOIN tenants AS t ON t.id = tsv.tenant_id AND t.deleted_at IS NULL").
		Where("tsv.current_version < tsv.target_version").
		Where("tsv.status IN ?", []string{TenantSchemaStatusPending, TenantSchemaStatusFailed, TenantSchemaStatusDrifted}).
		Where("(tsv.migration_lock IS NULL OR tsv.lock_expires_at IS NULL OR tsv.lock_expires_at < ?)", now)
	if HasTenantSchemaNextRetryColumn() {
		q = q.Where("(tsv.next_retry_at IS NULL OR tsv.next_retry_at <= ?)", now)
	}
	q = q.Order("tsv.attempts ASC, tsv.updated_at ASC")
	if activeOnly {
		q = q.Where("t.status = ?", "active")
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []PendingTenantRow
	return rows, q.Scan(&rows).Error
}

// TryAcquireSchemaMigrationLock evita doble worker en el mismo tenant.
func TryAcquireSchemaMigrationLock(tenantID uint, workerID string, lease time.Duration) (bool, error) {
	now := time.Now()
	expires := now.Add(lease)
	res := CentralDB.Model(&TenantSchemaVersion{}).
		Where("tenant_id = ?", tenantID).
		Where("(migration_lock IS NULL OR lock_expires_at IS NULL OR lock_expires_at < ?)", now).
		Updates(map[string]interface{}{
			"migration_lock":  workerID,
			"lock_expires_at": expires,
			"status":          TenantSchemaStatusRunning,
			"updated_at":      now,
		})
	return res.RowsAffected == 1, res.Error
}

// ReleaseSchemaMigrationLock libera lease.
func ReleaseSchemaMigrationLock(tenantID uint) error {
	return CentralDB.Model(&TenantSchemaVersion{}).
		Where("tenant_id = ?", tenantID).
		Updates(map[string]interface{}{
			"migration_lock":  nil,
			"lock_expires_at": nil,
		}).Error
}
