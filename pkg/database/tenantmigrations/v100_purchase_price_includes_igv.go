package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v100Purchase struct {
	PriceIncludesIgv bool `gorm:"column:price_includes_igv;default:false"`
}

func (v100Purchase) TableName() string { return "tenant_purchases" }

type v100PurchaseItem struct {
	PriceIncludesIgv bool `gorm:"column:price_includes_igv;default:false"`
}

func (v100PurchaseItem) TableName() string { return "tenant_purchase_items" }

// V100PurchasePriceIncludesIgv guarda si el costo tecleado en la compra ya incluía IGV.
// Sin esta columna, al reabrir una compra no se puede saber con qué criterio se registró.
// El default false conserva el comportamiento histórico: el IGV se sumaba encima del costo.
type V100PurchasePriceIncludesIgv struct{}

func (V100PurchasePriceIncludesIgv) Version() int { return 100 }
func (V100PurchasePriceIncludesIgv) Name() string { return "purchase_price_includes_igv" }

func (V100PurchasePriceIncludesIgv) Up(db *gorm.DB) error {
	mig := db.Migrator()

	head := &v100Purchase{}
	if !mig.HasColumn(head, "PriceIncludesIgv") {
		if err := mig.AddColumn(head, "PriceIncludesIgv"); err != nil {
			return fmt.Errorf("add tenant_purchases.price_includes_igv: %w", err)
		}
	}

	item := &v100PurchaseItem{}
	if !mig.HasColumn(item, "PriceIncludesIgv") {
		if err := mig.AddColumn(item, "PriceIncludesIgv"); err != nil {
			return fmt.Errorf("add tenant_purchase_items.price_includes_igv: %w", err)
		}
	}

	return nil
}
