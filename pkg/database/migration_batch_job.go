package database

import "time"

const (
	MigrationJobStatusPending   = "pending"
	MigrationJobStatusRunning   = "running"
	MigrationJobStatusCompleted = "completed"
	MigrationJobStatusFailed    = "failed"
)

const (
	MigrationJobKindDriftScan      = "drift_scan"
	MigrationJobKindRepairSelected = "repair_selected"
	MigrationJobKindRepairDrifted  = "repair_drifted"
	MigrationJobKindRetryFailed    = "retry_failed"
)

// MigrationBatchJob seguimiento de operaciones masivas desde el panel.
type MigrationBatchJob struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	Kind        string     `gorm:"size:32;not null;index" json:"kind"`
	Status      string     `gorm:"size:20;not null;default:'pending';index" json:"status"`
	Total       int        `gorm:"not null;default:0" json:"total"`
	Processed   int        `gorm:"not null;default:0" json:"processed"`
	Succeeded   int        `gorm:"not null;default:0" json:"succeeded"`
	Failed      int        `gorm:"not null;default:0" json:"failed"`
	Payload     string     `gorm:"type:text" json:"payload,omitempty"`
	Results     string     `gorm:"type:longtext" json:"results,omitempty"`
	Error       string     `gorm:"type:text" json:"error,omitempty"`
	CreatedBy   uint       `gorm:"not null;default:0" json:"created_by"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (MigrationBatchJob) TableName() string { return "migration_batch_jobs" }
