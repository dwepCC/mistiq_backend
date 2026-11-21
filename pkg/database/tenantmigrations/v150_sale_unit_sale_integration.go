package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v150SaleItem struct {
	ID         uint  `gorm:"primaryKey"`
	SaleUnitID *uint `gorm:"column:sale_unit_id;index"`
}

func (v150SaleItem) TableName() string { return "tenant_sale_items" }

type v150StockMovement struct {
	ID               uint     `gorm:"primaryKey"`
	SaleUnitID       *uint    `gorm:"column:sale_unit_id;index"`
	SaleUnitQuantity *float64 `gorm:"column:sale_unit_quantity;type:decimal(15,3)"`
	ConversionFactor *float64 `gorm:"column:conversion_factor;type:decimal(15,6)"`
}

func (v150StockMovement) TableName() string { return "tenant_stock_movements" }

// V150SaleUnitSaleIntegration conecta TenantProductSaleUnit (Fase 1) con ventas y Kardex: agrega
// sale_unit_id a tenant_sale_items (qué unidad de venta se usó, cantidad comercial ya vive en
// quantity) y sale_unit_id/sale_unit_quantity/conversion_factor a tenant_stock_movements (snapshot
// histórico inmutable de la conversión — tenant_stock_movements.quantity sigue siendo, como
// siempre, la cantidad en unidad base). Puramente aditivo: columnas nullable, nada existente
// cambia de significado ni de valor.
type V150SaleUnitSaleIntegration struct{}

func (V150SaleUnitSaleIntegration) Version() int { return 150 }
func (V150SaleUnitSaleIntegration) Name() string { return "sale_unit_sale_integration" }

func (V150SaleUnitSaleIntegration) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if mig.HasTable(&v150SaleItem{}) && !mig.HasColumn(&v150SaleItem{}, "SaleUnitID") {
		if err := mig.AddColumn(&v150SaleItem{}, "SaleUnitID"); err != nil {
			return fmt.Errorf("add tenant_sale_items.sale_unit_id: %w", err)
		}
	}
	if mig.HasTable(&v150StockMovement{}) {
		if !mig.HasColumn(&v150StockMovement{}, "SaleUnitID") {
			if err := mig.AddColumn(&v150StockMovement{}, "SaleUnitID"); err != nil {
				return fmt.Errorf("add tenant_stock_movements.sale_unit_id: %w", err)
			}
		}
		if !mig.HasColumn(&v150StockMovement{}, "SaleUnitQuantity") {
			if err := mig.AddColumn(&v150StockMovement{}, "SaleUnitQuantity"); err != nil {
				return fmt.Errorf("add tenant_stock_movements.sale_unit_quantity: %w", err)
			}
		}
		if !mig.HasColumn(&v150StockMovement{}, "ConversionFactor") {
			if err := mig.AddColumn(&v150StockMovement{}, "ConversionFactor"); err != nil {
				return fmt.Errorf("add tenant_stock_movements.conversion_factor: %w", err)
			}
		}
	}
	return nil
}
