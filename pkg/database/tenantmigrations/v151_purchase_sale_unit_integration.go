package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v151PurchaseItem struct {
	ID         uint  `gorm:"primaryKey"`
	SaleUnitID *uint `gorm:"column:sale_unit_id;index"`
}

func (v151PurchaseItem) TableName() string { return "tenant_purchase_items" }

type v151StockMovement struct {
	ID             uint  `gorm:"primaryKey"`
	PurchaseItemID *uint `gorm:"column:purchase_item_id;index"`
}

func (v151StockMovement) TableName() string { return "tenant_stock_movements" }

// V151PurchaseSaleUnitIntegration conecta TenantProductSaleUnit (Fase 1) con compras y Kardex,
// simétrico a V150 (ventas):
//   - tenant_purchase_items.sale_unit_id: qué unidad de venta se usó en la línea (cantidad/costo
//     comercial siguen viviendo en quantity/unit_cost de esa misma fila, sin cambio de semántica).
//   - tenant_stock_movements.purchase_item_id: enlace directo a la línea de compra que originó el
//     movimiento (mismo rol que sale_item_id para ventas) — necesario para que una reversión
//     (PurchaseService.Void) pueda releer la cantidad/costo BASE ya persistidos en el movimiento
//     histórico en vez de recalcularlos desde la cantidad/costo COMERCIALES de la línea.
//   - amplía tenant_stock_movements.unit_cost y tenant_products.purchase_price a DECIMAL(15,6):
//     un costo comercial dividido entre un factor de conversión (ej. S/100 ÷ 24 = S/4.1666...)
//     puede necesitar más de 2 decimales exactos; decimal(15,2) los trunca con pérdida de valor
//     sistemática. Mismo criterio ya aplicado en V047SaleAmountsSunatPrecision. Es un ensanche de
//     precisión, no un cambio de escala: todo valor con 2 decimales existente cabe exacto en
//     decimal(15,6) (3.50 → 3.500000), así que no hay pérdida ni reinterpretación de datos
//     existentes.
//
// Todo lo demás (columnas nuevas nullable, sin backfill) es aditivo: compras existentes con
// sale_unit_id NULL y purchase_item_id NULL en su kardex siguen siendo válidas y no cambian de
// comportamiento.
type V151PurchaseSaleUnitIntegration struct{}

func (V151PurchaseSaleUnitIntegration) Version() int { return 151 }
func (V151PurchaseSaleUnitIntegration) Name() string { return "purchase_sale_unit_integration" }

func (V151PurchaseSaleUnitIntegration) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if mig.HasTable(&v151PurchaseItem{}) && !mig.HasColumn(&v151PurchaseItem{}, "SaleUnitID") {
		if err := mig.AddColumn(&v151PurchaseItem{}, "SaleUnitID"); err != nil {
			return fmt.Errorf("add tenant_purchase_items.sale_unit_id: %w", err)
		}
	}
	if mig.HasTable(&v151StockMovement{}) && !mig.HasColumn(&v151StockMovement{}, "PurchaseItemID") {
		if err := mig.AddColumn(&v151StockMovement{}, "PurchaseItemID"); err != nil {
			return fmt.Errorf("add tenant_stock_movements.purchase_item_id: %w", err)
		}
	}

	alters := []struct {
		table    string
		column   string
		ddlExtra string
	}{
		{"tenant_stock_movements", "unit_cost", ""},
		{"tenant_products", "purchase_price", ""},
	}
	for _, a := range alters {
		if !mig.HasTable(a.table) || !mig.HasColumn(a.table, a.column) {
			continue
		}
		sql := fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s DECIMAL(15,6)%s", a.table, a.column, a.ddlExtra)
		if err := db.Exec(sql).Error; err != nil {
			return fmt.Errorf("%s.%s: %w", a.table, a.column, err)
		}
	}
	return nil
}
