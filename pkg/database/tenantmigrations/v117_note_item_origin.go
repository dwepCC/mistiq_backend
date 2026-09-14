package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v117SaleItem struct {
	OriginalSaleItemID *uint `gorm:"column:original_sale_item_id;index"`
}

func (v117SaleItem) TableName() string { return "tenant_sale_items" }

// V117NoteItemOrigin agrega tenant_sale_items.original_sale_item_id: en una nota de
// crédito parcial (Fase 2 — selección de ítems/cantidades), cada línea de la nota nace de
// una línea específica de la venta original. Sin este vínculo, revertir el stock exacto de
// lo que la nota devuelve (y solo eso) no se puede hacer de forma confiable — el kardex
// referencia sale_item_id, así que hace falta saber a qué ítem original corresponde cada
// línea de la nota para consultarlo. NULL en toda nota que copia el 100% (motivo "01" y
// los que no mueven bienes) — ahí no hace falta, se sigue revirtiendo por venta completa.
type V117NoteItemOrigin struct{}

func (V117NoteItemOrigin) Version() int { return 117 }
func (V117NoteItemOrigin) Name() string { return "note_item_origin" }

func (V117NoteItemOrigin) Up(db *gorm.DB) error {
	mig := db.Migrator()
	row := &v117SaleItem{}
	if !mig.HasColumn(row, "OriginalSaleItemID") {
		if err := mig.AddColumn(row, "OriginalSaleItemID"); err != nil {
			return fmt.Errorf("add tenant_sale_items.original_sale_item_id: %w", err)
		}
	}
	return nil
}
