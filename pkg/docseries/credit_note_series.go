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

// ValidateSeriesConfig reglas por categoría / código SUNAT (series y comprobantes).
func ValidateSeriesConfig(category, sunatCode, seriesName string) error {
	cat := strings.TrimSpace(strings.ToLower(category))
	sc := strings.TrimSpace(sunatCode)
	name := NormalizeSeriesCode(seriesName)
	if cat == "nota_credito" || sc == "07" {
		if cat != "" && cat != "nota_credito" {
			return fmt.Errorf("categoría %q no corresponde a nota de crédito (SUNAT 07)", category)
		}
		if sc != "" && sc != "07" {
			return fmt.Errorf("código SUNAT %q no corresponde a nota de crédito (use 07)", sunatCode)
		}
		return ValidateNotaCreditoSeriesCode(name)
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
