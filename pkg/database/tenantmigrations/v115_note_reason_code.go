package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v115Sale struct {
	NoteReasonCode string `gorm:"column:note_reason_code;size:5"`
}

func (v115Sale) TableName() string { return "tenant_sales" }

// V115NoteReasonCode agrega el código de motivo SUNAT (catálogo 09 para nota de crédito,
// catálogo 10 para nota de débito) elegido al emitir la nota.
//
// Antes codMotivo quedaba fijo en el código: "01" (anulación de la operación) para toda NC
// y "02" (aumento en el valor) para toda ND, sin importar lo que el usuario indicara — el
// catálogo completo de motivos (pkg/sunatnote) existía pero solo se usaba para mostrar la
// etiqueta después de emitida, nunca para elegirla. note_reason_code persiste el motivo
// real elegido; vacío (notas emitidas antes de esta migración) se sigue interpretando como
// el comportamiento anterior — ver buildNotePayload y PostFiscalAcceptSideEffects.
type V115NoteReasonCode struct{}

func (V115NoteReasonCode) Version() int { return 115 }
func (V115NoteReasonCode) Name() string { return "note_reason_code" }

func (V115NoteReasonCode) Up(db *gorm.DB) error {
	mig := db.Migrator()
	row := &v115Sale{}
	if !mig.HasColumn(row, "NoteReasonCode") {
		if err := mig.AddColumn(row, "NoteReasonCode"); err != nil {
			return fmt.Errorf("add tenant_sales.note_reason_code: %w", err)
		}
	}
	return nil
}
