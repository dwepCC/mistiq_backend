package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v172PurchaseItem struct {
	ID             uint  `gorm:"primaryKey"`
	PresentationID *uint `gorm:"column:presentation_id;index"`
}

func (v172PurchaseItem) TableName() string { return "tenant_purchase_items" }

// V172PurchaseItemPresentation agrega tenant_purchase_items.presentation_id: la presentación o
// variante comprada en cada línea (ej. "Talla M"), para ingresar el stock a esa presentación.
// Columna nullable sin backfill: las compras existentes quedan igual. Idempotente.
type V172PurchaseItemPresentation struct{}

func (V172PurchaseItemPresentation) Version() int { return 172 }
func (V172PurchaseItemPresentation) Name() string { return "purchase_item_presentation" }

func (V172PurchaseItemPresentation) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v172PurchaseItem{}) || mig.HasColumn(&v172PurchaseItem{}, "PresentationID") {
		return nil
	}
	if err := mig.AddColumn(&v172PurchaseItem{}, "PresentationID"); err != nil {
		return fmt.Errorf("add tenant_purchase_items.presentation_id: %w", err)
	}
	return nil
}
