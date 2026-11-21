package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v160SaleItem struct {
	ID            uint     `gorm:"primaryKey"`
	PurchasePrice *float64 `gorm:"column:purchase_price;type:decimal(15,6)"`
}

func (v160SaleItem) TableName() string { return "tenant_sale_items" }

// V160SaleItemPurchasePriceSnapshot agrega purchase_price a tenant_sale_items: snapshot del costo
// del producto (TenantProduct.PurchasePrice) al momento de venderse esa línea, para que el reporte
// de Utilidades (/reports/profit, ver internal/sales/service/sale_service.go ProfitDetail) calcule
// la ganancia con el costo REAL de ese día — antes usaba el costo ACTUAL del catálogo (join en
// vivo), así que si el costo de un producto cambiaba después, las ventas pasadas de ese producto
// recalculaban su ganancia con el costo nuevo en vez del histórico.
//
// Puramente aditiva: columna nullable, nada existente cambia de significado. Las líneas de venta
// creadas ANTES de esta migración quedan con purchase_price=NULL — ProfitDetail cae al costo
// actual del catálogo como resguardo en ese caso (ver COALESCE en salesByProduct... ProfitDetail).
// Ver internal/sales/service/sale_service_calc.go (resolveSaleItemPurchasePrice) y
// internal/restaurant/service/restaurant_service.go para dónde se rellena de ahora en adelante.
type V160SaleItemPurchasePriceSnapshot struct{}

func (V160SaleItemPurchasePriceSnapshot) Version() int { return 160 }
func (V160SaleItemPurchasePriceSnapshot) Name() string { return "sale_item_purchase_price_snapshot" }

func (V160SaleItemPurchasePriceSnapshot) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v160SaleItem{}) {
		return nil
	}
	if !mig.HasColumn(&v160SaleItem{}, "PurchasePrice") {
		if err := mig.AddColumn(&v160SaleItem{}, "PurchasePrice"); err != nil {
			return fmt.Errorf("add tenant_sale_items.purchase_price: %w", err)
		}
	}
	return nil
}
