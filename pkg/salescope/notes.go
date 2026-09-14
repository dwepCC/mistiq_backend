package salescope

import "strings"

// Tipos de documento nota (crédito/débito): anulan o ajustan un comprobante ya emitido, nunca
// generan una venta/ingreso nuevo — un reporte comercial que las cuente como venta las duplica
// (la venta original ya está en el reporte; la nota es el reverso, no un hecho aparte).
const (
	DocTypeCreditNote = "NOTA_CREDITO"
	DocTypeDebitNote  = "NOTA_DEBITO"
)

// NoteDocTypes valores de tenant_sales.doc_type que son nota — para excluirlos con
// `.Where("doc_type NOT IN ?", salescope.NoteDocTypes)` en reportes que no deben tratarlos como
// venta. No reemplaza a CommercialSales/ScopeCommercial (eso filtra por sale_origin, esto por
// doc_type); son criterios distintos que se aplican juntos donde haga falta.
var NoteDocTypes = []string{DocTypeCreditNote, DocTypeDebitNote}

// IsNoteDocType true si docType es una nota de crédito o débito.
func IsNoteDocType(docType string) bool {
	dt := strings.ToUpper(strings.TrimSpace(docType))
	return dt == DocTypeCreditNote || dt == DocTypeDebitNote
}
