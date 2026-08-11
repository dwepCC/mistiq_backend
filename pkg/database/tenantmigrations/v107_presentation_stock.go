package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v107ProductPresentationStock struct {
	ID             uint `gorm:"primaryKey"`
	PresentationID uint `gorm:"not null;index"`
	BranchID       uint `gorm:"not null;index"`
	Quantity       float64
	UpdatedAt      time.Time
}

func (v107ProductPresentationStock) TableName() string { return "tenant_product_presentation_stocks" }

type v107StockMovement struct {
	ID             uint  `gorm:"primaryKey"`
	PresentationID *uint `gorm:"column:presentation_id;index"`
}

func (v107StockMovement) TableName() string { return "tenant_stock_movements" }

type v107SaleItem struct {
	ID             uint  `gorm:"primaryKey"`
	PresentationID *uint `gorm:"column:presentation_id;index"`
}

func (v107SaleItem) TableName() string { return "tenant_sale_items" }

type v107InventoryDocumentDetail struct {
	ID             uint  `gorm:"primaryKey"`
	PresentationID *uint `gorm:"column:presentation_id;index"`
}

func (v107InventoryDocumentDetail) TableName() string { return "tenant_inventory_document_details" }

// V107PresentationStock agrega stock por variante/presentación (ej. color, talla): un producto
// con presentaciones (has_variants) puede tener 5 unidades rojo + 3 azul + 2 amarillo en vez de
// un único total de 10 que se descontaba sin importar cuál se vendió.
type V107PresentationStock struct{}

func (V107PresentationStock) Version() int { return 107 }
func (V107PresentationStock) Name() string { return "presentation_stock" }

func (V107PresentationStock) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v107ProductPresentationStock{}) {
		if err := mig.CreateTable(&v107ProductPresentationStock{}); err != nil {
			return fmt.Errorf("create tenant_product_presentation_stocks: %w", err)
		}
	}
	if mig.HasTable(&v107StockMovement{}) && !mig.HasColumn(&v107StockMovement{}, "PresentationID") {
		if err := mig.AddColumn(&v107StockMovement{}, "PresentationID"); err != nil {
			return fmt.Errorf("add tenant_stock_movements.presentation_id: %w", err)
		}
	}
	if mig.HasTable(&v107SaleItem{}) && !mig.HasColumn(&v107SaleItem{}, "PresentationID") {
		if err := mig.AddColumn(&v107SaleItem{}, "PresentationID"); err != nil {
			return fmt.Errorf("add tenant_sale_items.presentation_id: %w", err)
		}
	}
	if mig.HasTable(&v107InventoryDocumentDetail{}) && !mig.HasColumn(&v107InventoryDocumentDetail{}, "PresentationID") {
		if err := mig.AddColumn(&v107InventoryDocumentDetail{}, "PresentationID"); err != nil {
			return fmt.Errorf("add tenant_inventory_document_details.presentation_id: %w", err)
		}
	}
	return nil
}
