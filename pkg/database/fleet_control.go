package database

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// FleetMigrationState control global del fleet (fila única ID=1).
type FleetMigrationState struct {
	ID                     uint       `gorm:"primaryKey"`
	CircuitOpen            bool       `gorm:"not null;default:false"`
	CircuitReason          string     `gorm:"type:text"`
	OpenedAt               *time.Time `json:"opened_at,omitempty"`
	LastFleetRunAt         *time.Time `json:"last_fleet_run_at,omitempty"`
	AvgMigrationDurationMs int64    `gorm:"not null;default:0" json:"avg_migration_duration_ms"`
	UpdatedAt              time.Time
}

func (FleetMigrationState) TableName() string { return "fleet_migration_state" }

// EnsureFleetMigrationState crea tabla y fila default.
func EnsureFleetMigrationState() error {
	if CentralDB == nil {
		return errors.New("BD central no conectada")
	}
	var row FleetMigrationState
	err := CentralDB.First(&row, 1).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return CentralDB.Create(&FleetMigrationState{ID: 1, CircuitOpen: false}).Error
}

// IsFleetCircuitOpen indica si el circuit breaker detuvo el fleet.
func IsFleetCircuitOpen() (open bool, reason string, err error) {
	if CentralDB == nil {
		return false, "", errors.New("BD central no conectada")
	}
	if err := EnsureFleetMigrationState(); err != nil {
		return false, "", err
	}
	var row FleetMigrationState
	if err := CentralDB.First(&row, 1).Error; err != nil {
		return false, "", err
	}
	return row.CircuitOpen, row.CircuitReason, nil
}

// TripFleetCircuitBreaker pausa migraciones fleet globalmente.
func TripFleetCircuitBreaker(reason string) error {
	if CentralDB == nil {
		return errors.New("BD central no conectada")
	}
	if err := EnsureFleetMigrationState(); err != nil {
		return err
	}
	now := time.Now()
	return CentralDB.Model(&FleetMigrationState{}).Where("id = ?", 1).
		Updates(map[string]interface{}{
			"circuit_open":   true,
			"circuit_reason": reason,
			"opened_at":      now,
			"updated_at":     now,
		}).Error
}

// LastFleetCircuitReason lee motivo del circuit breaker abierto.
func LastFleetCircuitReason() string {
	if CentralDB == nil {
		return ""
	}
	var row FleetMigrationState
	if err := CentralDB.First(&row, 1).Error; err != nil {
		return ""
	}
	if !row.CircuitOpen {
		return ""
	}
	return row.CircuitReason
}

// RecordFleetRunComplete guarda métricas del último ciclo fleet.
func RecordFleetRunComplete(avgDurationMs int64) {
	if CentralDB == nil {
		return
	}
	_ = EnsureFleetMigrationState()
	now := time.Now()
	updates := map[string]interface{}{
		"last_fleet_run_at": now,
		"updated_at":      now,
	}
	if avgDurationMs > 0 {
		updates["avg_migration_duration_ms"] = avgDurationMs
	}
	_ = CentralDB.Model(&FleetMigrationState{}).Where("id = ?", 1).Updates(updates).Error
}

// ResetFleetCircuitBreaker reanuda fleet tras intervención ops.
func ResetFleetCircuitBreaker() error {
	if CentralDB == nil {
		return errors.New("BD central no conectada")
	}
	if err := EnsureFleetMigrationState(); err != nil {
		return err
	}
	now := time.Now()
	return CentralDB.Model(&FleetMigrationState{}).Where("id = ?", 1).
		Updates(map[string]interface{}{
			"circuit_open":   false,
			"circuit_reason": "",
			"opened_at":      nil,
			"updated_at":     now,
		}).Error
}
