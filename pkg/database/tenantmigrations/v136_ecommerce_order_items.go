package tenantmigrations

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// v136Order espejo local mínimo de TenantEcommerceOrder para leer ItemsJSON sin importar el
// paquete service (evitaría un ciclo: service ya importa database, y tenantmigrations no debe
// depender de paquetes de dominio).
type v136Order struct {
	ID        uint
	ItemsJSON string `gorm:"column:items_json"`
}

func (v136Order) TableName() string { return "tenant_ecommerce_orders" }

// v136Item mismo shape que database.TenantEcommerceOrderItem.
type v136Item struct {
	ID             uint `gorm:"primaryKey"`
	OrderID        uint
	ProductID      uint
	PresentationID *uint
	Name           string
	Quantity       float64
	UnitPrice      float64
	Subtotal       float64
	CreatedAt      time.Time
}

func (v136Item) TableName() string { return "tenant_ecommerce_order_items" }

// v136ItemJSON forma de cada entrada en ItemsJSON — mismo shape que
// internal/ecommerce/service.OrderItemInput (duplicado a propósito, ver comentario en v136Order).
type v136ItemJSON struct {
	ProductID uint    `json:"product_id"`
	Name      string  `json:"name"`
	Quantity  float64 `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
}

// V136EcommerceOrderItems crea tenant_ecommerce_order_items (Contrato v2 §1.2) y hace backfill
// histórico: deserializa el ItemsJSON de cada pedido existente y crea sus filas normalizadas, para
// que TODO pedido (viejo o nuevo) tenga líneas reales en la tabla nueva — ConvertToSale y la
// futura vista de picking (Fase 7) pueden leer siempre de acá, con fallback a ItemsJSON solo si
// algún pedido tuviera JSON corrupto (no se ha encontrado ninguno, es una red de seguridad).
// ItemsJSON NO se borra ni se deja de escribir — ver comentario en TenantEcommerceOrder.
type V136EcommerceOrderItems struct{}

func (V136EcommerceOrderItems) Version() int { return 136 }
func (V136EcommerceOrderItems) Name() string { return "ecommerce_order_items" }

func (V136EcommerceOrderItems) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v136Item{}) {
		if err := mig.CreateTable(&v136Item{}); err != nil {
			return fmt.Errorf("tenant_ecommerce_order_items: %w", err)
		}
	}
	if !mig.HasTable(&v136Order{}) {
		return nil
	}

	var alreadyBackfilled int64
	if err := db.Model(&v136Item{}).Count(&alreadyBackfilled).Error; err != nil {
		return err
	}
	if alreadyBackfilled > 0 {
		return nil // ya se corrió (idempotencia: no duplicar líneas en un re-run)
	}

	var orders []v136Order
	if err := db.Find(&orders).Error; err != nil {
		return fmt.Errorf("v136 leer pedidos: %w", err)
	}
	now := time.Now()
	for _, order := range orders {
		var raw []v136ItemJSON
		if err := json.Unmarshal([]byte(order.ItemsJSON), &raw); err != nil || len(raw) == 0 {
			continue // JSON vacío/corrupto: el pedido queda sin líneas normalizadas, ItemsJSON
			// sigue siendo la fuente de verdad para ese caso puntual (fallback en ConvertToSale)
		}
		for _, it := range raw {
			if it.ProductID == 0 || it.Quantity <= 0 {
				continue
			}
			row := v136Item{
				OrderID:   order.ID,
				ProductID: it.ProductID,
				Name:      it.Name,
				Quantity:  it.Quantity,
				UnitPrice: it.UnitPrice,
				Subtotal:  it.Quantity * it.UnitPrice,
				CreatedAt: now,
			}
			if err := db.Create(&row).Error; err != nil {
				return fmt.Errorf("v136 backfill order #%d: %w", order.ID, err)
			}
		}
	}
	return nil
}
