package docseries

import (
	"fmt"
	"regexp"
	"strings"
)

// Serie SUNAT de nota de crédito: FC## anula facturas, BC## anula boletas (4 caracteres, ej. FC01, BC01).
var notaCreditoSeriesRE = regexp.MustCompile(`^(FC|BC)[0-9]{2}$`)

// CreditNoteSeriesPrefixForAffected devuelve "FC" (factura 01) o "BC" (boleta 03).
func CreditNoteSeriesPrefixForAffected(docType, sunatCode string) string {
	sc := strings.TrimSpace(sunatCode)
	dt := strings.ToUpper(strings.TrimSpace(docType))
	if dt == "FACTURA" || sc == "01" {
		return "FC"
	}
	return "BC"
}

// SeriesMatchesCreditNotePrefix indica si la serie de NC corresponde al comprobante afectado.
func SeriesMatchesCreditNotePrefix(seriesCode, expectedPrefix string) bool {
	code := NormalizeSeriesCode(seriesCode)
	prefix := strings.ToUpper(strings.TrimSpace(expectedPrefix))
	return prefix != "" && strings.HasPrefix(code, prefix)
}

// ValidateNotaCreditoSeriesCode valida formato FC## / BC## al crear o editar series.
func ValidateNotaCreditoSeriesCode(seriesName string) error {
	code := NormalizeSeriesCode(seriesName)
	if code == "" {
		return fmt.Errorf("serie de nota de crédito requerida")
	}
	if !notaCreditoSeriesRE.MatchString(code) {
		return fmt.Errorf(
			"serie de nota de crédito inválida %q: use FC## para anular facturas o BC## para anular boletas (ej. FC01, BC01)",
			code,
		)
	}
	return nil
}

// ValidateSeriesConfig valida coherencia tipo/categoría/código documental y reglas de formato de serie.
func ValidateSeriesConfig(docType, category, documentCode, seriesName string) error {
	if err := ValidateSeriesDocumentType(docType, documentCode, category); err != nil {
		return err
	}
	def, err := ResolveDocumentType(docType)
	if err != nil {
		return err
	}
	if def.Category == "nota_credito" {
		return ValidateNotaCreditoSeriesCode(seriesName)
	}
	return nil
}

// AffectedDocLabel texto corto para mensajes de error.
func AffectedDocLabel(docType, sunatCode string) string {
	if CreditNoteSeriesPrefixForAffected(docType, sunatCode) == "FC" {
		return "factura"
	}
	return "boleta"
}
