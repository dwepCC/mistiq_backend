package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v110QuotationItem struct {
	ID             uint  `gorm:"primaryKey"`
	PresentationID *uint `gorm:"column:presentation_id;index"`
}

func (v110QuotationItem) TableName() string { return "tenant_quotation_items" }

// V110QuotationItemPresentation agrega presentation_id a las líneas de cotización, para que al
// convertir una cotización de un producto con variantes en venta se descuente la presentación
// correcta — mismo fix ya aplicado a ventas, transferencias y comandas de restaurante.
type V110QuotationItemPresentation struct{}

func (V110QuotationItemPresentation) Version() int { return 110 }
func (V110QuotationItemPresentation) Name() string { return "quotation_item_presentation" }

func (V110QuotationItemPresentation) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v110QuotationItem{}) {
		return nil
	}
	if mig.HasColumn(&v110QuotationItem{}, "PresentationID") {
		return nil
	}
	if err := mig.AddColumn(&v110QuotationItem{}, "PresentationID"); err != nil {
		return fmt.Errorf("add tenant_quotation_items.presentation_id: %w", err)
	}
	return nil
}
