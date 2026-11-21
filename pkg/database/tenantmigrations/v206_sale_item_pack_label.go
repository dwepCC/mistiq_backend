package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v206SaleItem struct {
	PackLabel string `gorm:"column:pack_label;size:60"`
}

func (v206SaleItem) TableName() string { return "tenant_sale_items" }

type v206QuotationItem struct {
	PackLabel string `gorm:"column:pack_label;size:60"`
}

func (v206QuotationItem) TableName() string { return "tenant_quotation_items" }

// V206SaleItemPackLabel agrega tenant_sale_items.pack_label y tenant_quotation_items.pack_label: el contenido de la unidad de venta
// («12 UND» para una caja de 12) que el comprobante imprime entre paréntesis junto a la descripción. Solo agrega columnas
// nullable: los documentos existentes NO se tocan ni se reconstruyen (quedan sin paréntesis, como hasta hoy). Idempotente.
type V206SaleItemPackLabel struct{}

func (V206SaleItemPackLabel) Version() int { return 206 }
func (V206SaleItemPackLabel) Name() string { return "sale_item_pack_label" }

func (V206SaleItemPackLabel) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if sale := &(v206SaleItem{}); !mig.HasColumn(sale, "PackLabel") {
		if err := mig.AddColumn(sale, "PackLabel"); err != nil {
			return fmt.Errorf("add tenant_sale_items.pack_label: %w", err)
		}
	}
	if quote := &(v206QuotationItem{}); !mig.HasColumn(quote, "PackLabel") {
		if err := mig.AddColumn(quote, "PackLabel"); err != nil {
			return fmt.Errorf("add tenant_quotation_items.pack_label: %w", err)
		}
	}
	return nil
}
