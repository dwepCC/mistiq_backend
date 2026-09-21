package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v137StatusHistory struct {
	ID         uint `gorm:"primaryKey"`
	OrderID    uint
	FromStatus string
	ToStatus   string
	UserID     *uint
	Notes      string
	CreatedAt  time.Time
}

func (v137StatusHistory) TableName() string { return "tenant_ecommerce_order_status_history" }

// V137EcommerceOrderStatusHistory crea la bitácora de transiciones de pedidos (Contrato v2 §1.3).
// Sin backfill: no hay forma de reconstruir transiciones históricas que nunca se registraron — la
// bitácora empieza a llenarse desde que EcommerceService.UpdateOrderStatus se despliega (Fase 1).
type V137EcommerceOrderStatusHistory struct{}

func (V137EcommerceOrderStatusHistory) Version() int { return 137 }
func (V137EcommerceOrderStatusHistory) Name() string { return "ecommerce_order_status_history" }

func (V137EcommerceOrderStatusHistory) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v137StatusHistory{}) {
		if err := mig.CreateTable(&v137StatusHistory{}); err != nil {
			return fmt.Errorf("tenant_ecommerce_order_status_history: %w", err)
		}
	}
	return nil
}
