package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v045Comanda struct {
	ID              uint   `gorm:"primaryKey"`
	PreparationArea string `gorm:"size:50"`
	PrintedByID     *uint
}

func (v045Comanda) TableName() string { return "tenant_comandas" }

type v045TableOrder struct {
	ID          uint `gorm:"primaryKey"`
	PrintedAt   *time.Time
	PrintedByID *uint
}

func (v045TableOrder) TableName() string { return "tenant_table_orders" }

// V045RestaurantKitchenRounds snapshot de área en comanda y auditoría de impresión por ronda.
type V045RestaurantKitchenRounds struct{}

func (V045RestaurantKitchenRounds) Version() int { return 45 }
func (V045RestaurantKitchenRounds) Name() string { return "restaurant_kitchen_rounds" }

func (V045RestaurantKitchenRounds) Up(db *gorm.DB) error {
	comanda := &v045Comanda{}
	if !db.Migrator().HasColumn(comanda, "PreparationArea") {
		if err := db.Migrator().AddColumn(comanda, "PreparationArea"); err != nil {
			return fmt.Errorf("tenant_comandas.preparation_area: %w", err)
		}
	}
	if !db.Migrator().HasColumn(comanda, "PrintedByID") {
		if err := db.Migrator().AddColumn(comanda, "PrintedByID"); err != nil {
			return fmt.Errorf("tenant_comandas.printed_by_id: %w", err)
		}
	}
	order := &v045TableOrder{}
	if !db.Migrator().HasColumn(order, "PrintedAt") {
		if err := db.Migrator().AddColumn(order, "PrintedAt"); err != nil {
			return fmt.Errorf("tenant_table_orders.printed_at: %w", err)
		}
	}
	if !db.Migrator().HasColumn(order, "PrintedByID") {
		if err := db.Migrator().AddColumn(order, "PrintedByID"); err != nil {
			return fmt.Errorf("tenant_table_orders.printed_by_id: %w", err)
		}
	}
	if db.Migrator().HasTable("tenant_comandas") && db.Migrator().HasTable("tenant_products") {
		_ = db.Exec(`
			UPDATE tenant_comandas c
			INNER JOIN tenant_products p ON p.id = c.product_id
			SET c.preparation_area = LOWER(TRIM(COALESCE(NULLIF(p.preparation_area,''), 'cocina')))
			WHERE (c.preparation_area IS NULL OR TRIM(c.preparation_area) = '')
			  AND c.product_id IS NOT NULL
		`).Error
	}
	return nil
}
