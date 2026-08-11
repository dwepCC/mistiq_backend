package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V046TableOrderPrintedAtDatetime corrige printed_at en tenant_table_orders (v045 lo creó como texto).
type V046TableOrderPrintedAtDatetime struct{}

func (V046TableOrderPrintedAtDatetime) Version() int { return 46 }
func (V046TableOrderPrintedAtDatetime) Name() string { return "table_order_printed_at_datetime" }

func (V046TableOrderPrintedAtDatetime) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_table_orders") {
		return nil
	}
	if !db.Migrator().HasColumn("tenant_table_orders", "printed_at") {
		return nil
	}
	// Limpiar valores no parseables antes del cambio de tipo.
	_ = db.Exec(`
		UPDATE tenant_table_orders
		SET printed_at = NULL
		WHERE printed_at IS NOT NULL
		  AND TRIM(CAST(printed_at AS CHAR)) = ''
	`).Error
	if err := db.Exec(`
		ALTER TABLE tenant_table_orders
		MODIFY COLUMN printed_at DATETIME(3) NULL
	`).Error; err != nil {
		return fmt.Errorf("tenant_table_orders.printed_at datetime: %w", err)
	}
	return nil
}
