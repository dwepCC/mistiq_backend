package database

import (
	"errors"
	"time"
)

// FleetMigrationSummary métricas agregadas del fleet.
type FleetMigrationSummary struct {
	Total               int64  `json:"total"`
	Completed           int64  `json:"completed"`
	Pending             int64  `json:"pending"`
	Failed              int64  `json:"failed"`
	Running             int64  `json:"running"`
	Paused              int64  `json:"paused"`
	Blocked             int64  `json:"blocked"`
	Drifted             int64  `json:"drifted"`
	Outdated            int64  `json:"outdated"`
	AvgMigrationMs      int64  `json:"avg_migration_duration_ms"`
	LastFleetRunAt      *time.Time `json:"last_fleet_run_at,omitempty"`
	SchemaTargetVersion int    `json:"schema_target_version"`
	WithoutRegistry     int64  `json:"without_registry"`
	CircuitOpen         bool   `json:"circuit_open"`
	CircuitReason       string `json:"circuit_reason,omitempty"`
}

// FleetMigrationSummaryQuery calcula resumen desde BD central.
func FleetMigrationSummaryQuery() (*FleetMigrationSummary, error) {
	if CentralDB == nil {
		return nil, errors.New("BD central no conectada")
	}
	target := TenantSchemaTargetVersion()
	sum := &FleetMigrationSummary{SchemaTargetVersion: target}

	CentralDB.Model(&Tenant{}).Where("deleted_at IS NULL").Count(&sum.Total)

	type st struct {
		Status string
		Cnt    int64
	}
	var rows []st
	CentralDB.Model(&TenantSchemaVersion{}).Select("status, COUNT(*) as cnt").Group("status").Scan(&rows)
	for _, r := range rows {
		switch r.Status {
		case TenantSchemaStatusCompleted:
			sum.Completed += r.Cnt
		case TenantSchemaStatusPending:
			sum.Pending += r.Cnt
		case TenantSchemaStatusFailed:
			sum.Failed += r.Cnt
		case TenantSchemaStatusRunning:
			sum.Running += r.Cnt
		case TenantSchemaStatusPaused:
			sum.Paused += r.Cnt
		case TenantSchemaStatusDrifted:
			sum.Drifted += r.Cnt
		}
	}

	var fleetState FleetMigrationState
	if err := CentralDB.First(&fleetState, 1).Error; err == nil {
		sum.LastFleetRunAt = fleetState.LastFleetRunAt
		if fleetState.AvgMigrationDurationMs > 0 {
			sum.AvgMigrationMs = fleetState.AvgMigrationDurationMs
		}
	}

	CentralDB.Model(&TenantSchemaVersion{}).Where("current_version < target_version").Count(&sum.Outdated)
	CentralDB.Table("tenants t").
		Joins("LEFT JOIN tenant_schema_versions tsv ON tsv.tenant_id = t.id").
		Where("t.deleted_at IS NULL AND tsv.tenant_id IS NULL").
		Count(&sum.WithoutRegistry)
	sum.Outdated += sum.WithoutRegistry

	if HasTenantSchemaNextRetryColumn() {
		now := time.Now()
		CentralDB.Model(&TenantSchemaVersion{}).
			Where("status = ? AND next_retry_at IS NOT NULL AND next_retry_at > ?", TenantSchemaStatusFailed, now).
			Count(&sum.Blocked)
	}

	if open, reason, err := IsFleetCircuitOpen(); err == nil {
		sum.CircuitOpen = open
		sum.CircuitReason = reason
	}
	return sum, nil
}

// RecoverStaleMigrationLocks libera locks expirados (self-healing).
func RecoverStaleMigrationLocks() int64 {
	if CentralDB == nil {
		return 0
	}
	now := time.Now()
	res := CentralDB.Model(&TenantSchemaVersion{}).
		Where("migration_lock IS NOT NULL AND lock_expires_at IS NOT NULL AND lock_expires_at < ?", now).
		Where("status = ?", TenantSchemaStatusRunning).
		Updates(map[string]interface{}{
			"status":          TenantSchemaStatusPending,
			"migration_lock":  nil,
			"lock_expires_at": nil,
			"updated_at":      now,
		})
	return res.RowsAffected
}
