package salescope

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

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

// notDocTypeExpr fragmento SQL "no es nota de crédito/débito" (alias ej. "s" o "tenant_sales").
func notDocTypeExpr(alias string) string {
	if strings.TrimSpace(alias) == "" {
		alias = defaultTable
	}
	return fmt.Sprintf("%s.doc_type NOT IN ('%s', '%s')", alias, DocTypeCreditNote, DocTypeDebitNote)
}

// CommercialSalesNoNotes es CommercialSales + excluye notas de crédito/débito. Para métricas de
// ventas (totales, conteos, rankings, por método, utilidades): una nota es el reverso de un
// comprobante ya contado, no una venta nueva, así que sumarla duplica el importe. Los LISTADOS de
// documentos siguen usando CommercialSales (la nota sí debe aparecer como documento).
func CommercialSalesNoNotes(db *gorm.DB) *gorm.DB {
	return CommercialSales(db).Where(notDocTypeExpr(defaultTable))
}

// ScopeCommercialNoNotes es ScopeCommercial(alias) + excluye notas de crédito/débito.
func ScopeCommercialNoNotes(alias string) func(*gorm.DB) *gorm.DB {
	commercial := ScopeCommercial(alias)
	return func(db *gorm.DB) *gorm.DB {
		return commercial(db).Where(notDocTypeExpr(alias))
	}
}

// CommercialWhereNoNotes es CommercialWhere(alias) + excluye notas, para consultas raw.
func CommercialWhereNoNotes(alias string) string {
	return CommercialWhere(alias) + " AND " + notDocTypeExpr(alias)
}
