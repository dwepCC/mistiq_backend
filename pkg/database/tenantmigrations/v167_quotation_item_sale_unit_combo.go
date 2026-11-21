package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v167QuotationItem struct {
	ID          uint   `gorm:"primaryKey"`
	SaleUnitID  *uint  `gorm:"column:sale_unit_id;index"`
	ComboJSON   string `gorm:"column:combo_json;type:text"`
	SerialsJSON string `gorm:"column:serials_json;type:text"`
}

func (v167QuotationItem) TableName() string { return "tenant_quotation_items" }

// V167QuotationItemSaleUnitCombo guarda en la línea de la cotización la unidad de venta, la
// elección de combo y los números de serie.
//
// Problema (revisión de cotizaciones, 05-oct-2026): el API de cotizaciones ignoraba estos tres
// datos. Cotizar 2 «Caja x12» y convertir vendía lo mismo que una venta directa pero descontaba 2
// unidades del inventario en vez de 24 (stock 49 → 23 en vez de → 25), sin ningún error; los
// combos no resolvían sus componentes y las series elegidas se descartaban.
//
// Aditiva: columnas nullable. Las cotizaciones anteriores quedan sin estos datos y siguen
// convirtiéndose igual que antes.
type V167QuotationItemSaleUnitCombo struct{}

func (V167QuotationItemSaleUnitCombo) Version() int { return 167 }
func (V167QuotationItemSaleUnitCombo) Name() string { return "quotation_item_sale_unit_combo" }

func (V167QuotationItemSaleUnitCombo) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable("tenant_quotation_items") {
		return nil
	}
	for _, col := range []string{"SaleUnitID", "ComboJSON", "SerialsJSON"} {
		if !mig.HasColumn(&v167QuotationItem{}, col) {
			if err := mig.AddColumn(&v167QuotationItem{}, col); err != nil {
				return fmt.Errorf("add tenant_quotation_items.%s: %w", col, err)
			}
		}
	}
	return nil
}
