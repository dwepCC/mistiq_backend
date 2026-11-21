package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v208OrderItem struct {
	ID            uint     `gorm:"primaryKey"`
	SaleUnitID    *uint    `gorm:"index"`
	ModifiersJSON string   `gorm:"type:text"`
	Detail        string   `gorm:"size:255"`
	BaseQuantity  *float64 `gorm:"type:decimal(15,6)"`
}

func (v208OrderItem) TableName() string { return "tenant_ecommerce_order_items" }

// V208EcommerceOrderItemOptions guarda en cada línea del pedido web la unidad de venta, los extras elegidos y la
// cantidad en unidad base (la reserva y la venta descuentan stock en unidad base).
type V208EcommerceOrderItemOptions struct{}

func (V208EcommerceOrderItemOptions) Version() int { return 208 }
func (V208EcommerceOrderItemOptions) Name() string { return "ecommerce_order_item_options" }

func (V208EcommerceOrderItemOptions) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v208OrderItem{}) {
		return nil
	}
	for _, col := range []string{"SaleUnitID", "ModifiersJSON", "Detail", "BaseQuantity"} {
		if mig.HasColumn(&v208OrderItem{}, col) {
			continue
		}
		if err := mig.AddColumn(&v208OrderItem{}, col); err != nil {
			return fmt.Errorf("tenant_ecommerce_order_items.%s: %w", col, err)
		}
	}
	return nil
}
