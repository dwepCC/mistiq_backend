package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v147Order struct {
	ID     uint   `gorm:"primaryKey"`
	Source string `gorm:"size:20;not null;default:'WEB'"`
}

func (v147Order) TableName() string { return "tenant_ecommerce_orders" }

// V147EcommerceOrderSource distingue los pedidos que llegan por la tienda (WEB) de los que el personal
// registra a mano en el panel (MANUAL: WhatsApp, llamada, mostrador).
type V147EcommerceOrderSource struct{}

func (V147EcommerceOrderSource) Version() int { return 147 }
func (V147EcommerceOrderSource) Name() string { return "ecommerce_order_source" }

func (V147EcommerceOrderSource) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v147Order{}) || mig.HasColumn(&v147Order{}, "Source") {
		return nil
	}
	if err := mig.AddColumn(&v147Order{}, "Source"); err != nil {
		return fmt.Errorf("tenant_ecommerce_orders.source: %w", err)
	}
	return nil
}
