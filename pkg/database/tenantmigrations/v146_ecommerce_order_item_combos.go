package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v146OrderItem struct {
	ID                  uint   `gorm:"primaryKey"`
	ComboJSON           string `gorm:"type:text"`
	ComboComponentsJSON string `gorm:"type:text"`
}

func (v146OrderItem) TableName() string { return "tenant_ecommerce_order_items" }

// V146EcommerceOrderItemCombos permite combos en los pedidos de la tienda: cada línea guarda la elección
// del cliente y el detalle de sus componentes (el stock se reserva y descuenta por componente).
type V146EcommerceOrderItemCombos struct{}

func (V146EcommerceOrderItemCombos) Version() int { return 146 }
func (V146EcommerceOrderItemCombos) Name() string { return "ecommerce_order_item_combos" }

func (V146EcommerceOrderItemCombos) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v146OrderItem{}) {
		return nil
	}
	for _, col := range []string{"ComboJSON", "ComboComponentsJSON"} {
		if mig.HasColumn(&v146OrderItem{}, col) {
			continue
		}
		if err := mig.AddColumn(&v146OrderItem{}, col); err != nil {
			return fmt.Errorf("tenant_ecommerce_order_items.%s: %w", col, err)
		}
	}
	return nil
}
