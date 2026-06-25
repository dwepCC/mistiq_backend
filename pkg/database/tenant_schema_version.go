package database

import "time"

// Estados de migración por tenant (BD central).
const (
	TenantSchemaStatusPending   = "pending"
	TenantSchemaStatusRunning   = "running"
	TenantSchemaStatusCompleted = "completed"
	TenantSchemaStatusFailed    = "failed"
	TenantSchemaStatusPaused    = "paused"
	TenantSchemaStatusDrifted   = "drifted"
)

// TenantSchemaVersion registro central del esquema de cada tenant.
type TenantSchemaVersion struct {
	TenantID        uint       `gorm:"primaryKey" json:"tenant_id"`
	CurrentVersion  int        `gorm:"not null;default:30" json:"current_version"`
	TargetVersion   int        `gorm:"not null;default:30" json:"target_version"`
	Status          string     `gorm:"size:20;not null;default:'completed'" json:"status"`
	MigrationLock   *string    `gorm:"size:64" json:"migration_lock,omitempty"`
	LockExpiresAt   *time.Time `json:"lock_expires_at,omitempty"`
	LastMigratedAt  *time.Time `json:"last_migrated_at,omitempty"`
	LastError       *string    `gorm:"type:text" json:"last_error,omitempty"`
	Attempts        int        `gorm:"not null;default:0" json:"attempts"`
	NextRetryAt     *time.Time `gorm:"index" json:"next_retry_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (TenantSchemaVersion) TableName() string { return "tenant_schema_versions" }
