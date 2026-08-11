package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v106EcommerceOrder struct {
	ID              uint       `gorm:"primaryKey"`
	ConvertedSaleID *uint      `gorm:"column:converted_sale_id"`
	ConvertedAt     *time.Time `gorm:"column:converted_at"`
}

func (v106EcommerceOrder) TableName() string { return "tenant_ecommerce_orders" }

// V106EcommerceOrderConversion agrega el rastro de conversión de un pedido web a una venta real
// (nota de venta/boleta/factura) generada desde el panel Tukifac.
type V106EcommerceOrderConversion struct{}

func (V106EcommerceOrderConversion) Version() int { return 106 }
func (V106EcommerceOrderConversion) Name() string { return "ecommerce_order_conversion" }

func (V106EcommerceOrderConversion) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v106EcommerceOrder{}) {
		return nil
	}
	for _, col := range []string{"ConvertedSaleID", "ConvertedAt"} {
		if mig.HasColumn(&v106EcommerceOrder{}, col) {
			continue
		}
		if err := mig.AddColumn(&v106EcommerceOrder{}, col); err != nil {
			return fmt.Errorf("add tenant_ecommerce_orders.%s: %w", col, err)
		}
	}
	return nil
}
