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
	// Success: SIN tag `default:...` a propósito — GORM omite del INSERT cualquier campo cuyo
	// valor sea el zero-value de Go (false, para bool) cuando el campo tiene un tag `default`,
	// dejando que la BD aplique su propio DEFAULT en su lugar. recordHistory (runner.go) y
	// RecordBackfillHistory (backfill.go) SIEMPRE pasan este valor explícito — incluyendo
	// `false` cuando una migración o backfill falla — así que un tag `default:true` aquí hacía
	// que TODO intento fallido se grabara como success=true, indistinguible de uno exitoso.
	// Encontrado en producción el 2026-09-06 (bug vivo desde 2026-07-20, commit 2bd830a0):
	// IsBackfillApplied/isHistoryApplied confiaban en esa columna para decidir si reintentar, y
	// nunca lo hacían porque todo fallo ya figuraba como éxito.
	Success  bool    `gorm:"not null" json:"success"`
	Error    *string `gorm:"type:text" json:"error,omitempty"`
	Checksum *string `gorm:"size:64" json:"checksum,omitempty"`
}

func (TenantMigrationHistory) TableName() string { return "tenant_migration_history" }
