package tenantbackfills

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// V032RestaurantOrdersBackfill tipos y códigos en sesiones existentes.
type V032RestaurantOrdersBackfill struct{}

func (V032RestaurantOrdersBackfill) Version() int { return 32 }
func (V032RestaurantOrdersBackfill) Name() string { return "restaurant_orders_backfill" }

func (V032RestaurantOrdersBackfill) Run(db *gorm.DB) error {
	if !hasColumn(db, "tenant_table_sessions", "order_type") {
		return fmt.Errorf("backfill: columnas de pedido restaurante no listas")
	}

	if err := db.Exec(`
		UPDATE tenant_table_sessions SET order_type = 'dine_in'
		WHERE (order_type IS NULL OR order_type = '') AND table_id IS NOT NULL
	`).Error; err != nil {
		return fmt.Errorf("backfill dine_in: %w", err)
	}

	if err := db.Exec(`
		UPDATE tenant_table_sessions SET order_type = 'quick_sale'
		WHERE (order_type IS NULL OR order_type = '') AND table_id IS NULL
	`).Error; err != nil {
		return fmt.Errorf("backfill quick_sale: %w", err)
	}

	if err := db.Exec(`
		UPDATE tenant_table_sessions SET order_status = 'paid'
		WHERE status IN ('billed', 'closed') AND (order_status IS NULL OR order_status = '' OR order_status = 'pending')
	`).Error; err != nil {
		return fmt.Errorf("backfill paid: %w", err)
	}

	if err := db.Exec(`
		UPDATE tenant_table_sessions SET order_status = 'cancelled'
		WHERE status = 'cancelled' AND (order_status IS NULL OR order_status = '')
	`).Error; err != nil {
		return fmt.Errorf("backfill cancelled: %w", err)
	}

	if err := db.Exec(`
		UPDATE tenant_table_sessions SET order_status = 'sent_to_kitchen'
		WHERE status = 'open' AND (order_status IS NULL OR order_status = '' OR order_status = 'pending')
		  AND id IN (SELECT DISTINCT session_id FROM tenant_comandas WHERE cancelled_at IS NULL)
	`).Error; err != nil {
		return fmt.Errorf("backfill sent_to_kitchen: %w", err)
	}

	if err := db.Exec(`
		UPDATE tenant_table_sessions SET order_status = 'pending'
		WHERE status = 'open' AND (order_status IS NULL OR order_status = '')
	`).Error; err != nil {
		return fmt.Errorf("backfill pending: %w", err)
	}

	// Códigos para sesiones abiertas sin código
	var rows []struct {
		ID       uint
		BranchID uint
		OpenedAt time.Time
	}
	if err := db.Table("tenant_table_sessions").
		Select("id, branch_id, opened_at").
		Where("order_code IS NULL OR order_code = ''").
		Find(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		code, err := nextOrderCodeForBackfill(db, r.BranchID, r.OpenedAt, r.ID)
		if err != nil {
			return err
		}
		if err := db.Exec(`UPDATE tenant_table_sessions SET order_code = ? WHERE id = ?`, code, r.ID).Error; err != nil {
			return err
		}
	}

	return nil
}

func nextOrderCodeForBackfill(db *gorm.DB, branchID uint, openedAt time.Time, sessionID uint) (string, error) {
	day := openedAt.Format("20060102")
	prefix := "P-" + day + "-"
	var count int64
	db.Table("tenant_table_sessions").
		Where("branch_id = ? AND order_code LIKE ? AND id < ?", branchID, prefix+"%", sessionID).
		Count(&count)
	return fmt.Sprintf("%s%04d", prefix, count+1), nil
}
