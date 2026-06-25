package service

import (
	"errors"
	"log"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const sessionStatusOpen = "open"

// findOpenSessionForTableLocked devuelve la sesión open más reciente para la mesa (requiere tx).
func (s *RestaurantService) findOpenSessionForTableLocked(tx *gorm.DB, tableID uint) (*database.TenantTableSession, error) {
	var sess database.TenantTableSession
	err := tx.Where("table_id = ? AND status = ?", tableID, sessionStatusOpen).
		Order("opened_at DESC, id DESC").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&sess).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// syncTableStatusFromOpenSession alinea tenant_restaurant_tables.status con sesiones open.
func (s *RestaurantService) syncTableStatusFromOpenSession(tx *gorm.DB, tableID uint) error {
	var openCount int64
	if err := tx.Model(&database.TenantTableSession{}).
		Where("table_id = ? AND status = ?", tableID, sessionStatusOpen).
		Count(&openCount).Error; err != nil {
		return err
	}
	target := "libre"
	if openCount > 0 {
		target = "ocupada"
	}
	return tx.Model(&database.TenantRestaurantTable{}).Where("id = ?", tableID).
		Update("status", target).Error
}

// resolveTableDisplayStatus deriva el estado visible: sesión open manda sobre table.status.
func resolveTableDisplayStatus(tableStatus string, sessionID *uint) string {
	if sessionID != nil && *sessionID > 0 {
		if tableStatus != "ocupada" {
			log.Printf("[restaurant] mesa desincronizada: table_status=%s session_id=%d → mostrar ocupada", tableStatus, *sessionID)
		}
		return "ocupada"
	}
	if tableStatus == "ocupada" {
		log.Printf("[restaurant] mesa desincronizada: table_status=ocupada sin sesión open → mostrar libre")
		return "libre"
	}
	return tableStatus
}

// isDuplicateOpenSessionError detecta violación del índice ux_open_session_per_table (MySQL 1062).
func isDuplicateOpenSessionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "1062") ||
		strings.Contains(msg, "ux_open_session_per_table")
}

// cancelStaleOpenSessions marca sesiones open duplicadas (conserva la más reciente).
func cancelStaleOpenSessions(tx *gorm.DB, tableID uint, keepSessionID uint) (int64, error) {
	now := time.Now()
	res := tx.Model(&database.TenantTableSession{}).
		Where("table_id = ? AND status = ? AND id <> ?", tableID, sessionStatusOpen, keepSessionID).
		Updates(map[string]interface{}{
			"status":    "cancelled",
			"closed_at": now,
		})
	return res.RowsAffected, res.Error
}
