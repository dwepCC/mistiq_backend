package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v118Sale struct {
	ManualAffectedDocType   string `gorm:"column:manual_affected_doc_type;size:5"`
	ManualAffectedDocNumber string `gorm:"column:manual_affected_doc_number;size:20"`
}

func (v118Sale) TableName() string { return "tenant_sales" }

// V118NoteManualAffectedDoc agrega el documento afectado declarado a mano (Fase 3 — emisión
// independiente de notas): tipo (01 factura / 03 boleta) y serie-número, para notas de
// crédito/débito que no nacen de una venta local — mismo rol que "data_affected_document" en
// el sistema legado. Vacío en cualquier nota que sí tiene original_sale_id.
type V118NoteManualAffectedDoc struct{}

func (V118NoteManualAffectedDoc) Version() int { return 118 }
func (V118NoteManualAffectedDoc) Name() string { return "note_manual_affected_doc" }

func (V118NoteManualAffectedDoc) Up(db *gorm.DB) error {
	mig := db.Migrator()
	row := &v118Sale{}
	if !mig.HasColumn(row, "ManualAffectedDocType") {
		if err := mig.AddColumn(row, "ManualAffectedDocType"); err != nil {
			return fmt.Errorf("add tenant_sales.manual_affected_doc_type: %w", err)
		}
	}
	if !mig.HasColumn(row, "ManualAffectedDocNumber") {
		if err := mig.AddColumn(row, "ManualAffectedDocNumber"); err != nil {
			return fmt.Errorf("add tenant_sales.manual_affected_doc_number: %w", err)
		}
	}
	return nil
}
