package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v101SaleItem struct {
	ItemNote string `gorm:"column:item_note;size:255"`
}

func (v101SaleItem) TableName() string { return "tenant_sale_items" }

type v101QuotationItem struct {
	ItemNote string `gorm:"column:item_note;size:255"`
}

func (v101QuotationItem) TableName() string { return "tenant_quotation_items" }

// V101SaleItemNote nota libre por línea de venta/cotización ("segundo uso", "sin caja", etc.).
// Vive en el snapshot del documento: no altera el producto del catálogo.
type V101SaleItemNote struct{}

func (V101SaleItemNote) Version() int { return 101 }
func (V101SaleItemNote) Name() string { return "sale_item_note" }

func (V101SaleItemNote) Up(db *gorm.DB) error {
	mig := db.Migrator()

	sale := &v101SaleItem{}
	if !mig.HasColumn(sale, "ItemNote") {
		if err := mig.AddColumn(sale, "ItemNote"); err != nil {
			return fmt.Errorf("add tenant_sale_items.item_note: %w", err)
		}
	}

	quote := &v101QuotationItem{}
	if !mig.HasColumn(quote, "ItemNote") {
		if err := mig.AddColumn(quote, "ItemNote"); err != nil {
			return fmt.Errorf("add tenant_quotation_items.item_note: %w", err)
		}
	}

	return nil
}
