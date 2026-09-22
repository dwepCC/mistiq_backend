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

// TableName debe coincidir EXACTAMENTE con la convención de pluralización de GORM que usa
// database.TenantEcommerceOrderStatusHistory (sin TableName() propio, así que GORM la resuelve a
// "tenant_ecommerce_order_status_histories") — bug real encontrado en Fase 11 (auditoría de
// producción, 2026-09-22): esta migración creaba la tabla en SINGULAR
// ("tenant_ecommerce_order_status_history"), un nombre distinto al que el modelo real usa en
// tiempo de ejecución, dejando el checkout de ecommerce roto (Error 1146: la tabla que el código
// buscaba nunca existía) en todo tenant que pasara por esta migración secuencial. Los tenants
// nuevos (AutoMigrate/baseline) nunca lo sufrieron porque usan el modelo real directamente.
func (v137StatusHistory) TableName() string { return "tenant_ecommerce_order_status_histories" }

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
			return fmt.Errorf("tenant_ecommerce_order_status_histories: %w", err)
		}
	}
	return nil
}
