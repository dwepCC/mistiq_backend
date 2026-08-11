package database

import "time"

const (
	MigrationHistoryTypeSchema   = "schema"
	MigrationHistoryTypeBackfill = "backfill"
)

// TenantMigrationHistory registro por tenant de migraciones/backfills aplicados.
type TenantMigrationHistory struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Version    int       `gorm:"not null;index:idx_tmh_version_type" json:"version"`
	Name       string    `gorm:"size:128;not null" json:"name"`
	Type       string    `gorm:"size:16;not null;index:idx_tmh_version_type" json:"type"`
	AppliedAt  time.Time `gorm:"not null" json:"applied_at"`
	DurationMs int64     `gorm:"not null;default:0" json:"duration_ms"`
	Success    bool      `gorm:"not null;default:true" json:"success"`
	Error      *string   `gorm:"type:text" json:"error,omitempty"`
	Checksum   *string   `gorm:"size:64" json:"checksum,omitempty"`
}

func (TenantMigrationHistory) TableName() string { return "tenant_migration_history" }
