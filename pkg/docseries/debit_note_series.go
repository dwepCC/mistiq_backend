package docseries

import (
	"fmt"
	"regexp"
	"strings"
)

// Serie SUNAT de nota de débito: FD## aumenta el valor de facturas, BD## el de boletas
// (4 caracteres, ej. FD01, BD01) — mismo esquema que las de nota de crédito (FC##/BC##).
var notaDebitoSeriesRE = regexp.MustCompile(`^(FD|BD)[0-9]{2}$`)

// DebitNoteSeriesPrefixForAffected devuelve "FD" (factura 01) o "BD" (boleta 03).
func DebitNoteSeriesPrefixForAffected(docType, sunatCode string) string {
	sc := strings.TrimSpace(sunatCode)
	dt := strings.ToUpper(strings.TrimSpace(docType))
	if dt == "FACTURA" || sc == "01" {
		return "FD"
	}
	return "BD"
}

// SeriesMatchesDebitNotePrefix indica si la serie de ND corresponde al comprobante afectado.
func SeriesMatchesDebitNotePrefix(seriesCode, expectedPrefix string) bool {
	code := NormalizeSeriesCode(seriesCode)
	prefix := strings.ToUpper(strings.TrimSpace(expectedPrefix))
	return prefix != "" && strings.HasPrefix(code, prefix)
}

// ValidateNotaDebitoSeriesCode valida formato FD## / BD## al crear o editar series.
func ValidateNotaDebitoSeriesCode(seriesName string) error {
	code := NormalizeSeriesCode(seriesName)
	if code == "" {
		return fmt.Errorf("serie de nota de débito requerida")
	}
	if !notaDebitoSeriesRE.MatchString(code) {
		return fmt.Errorf(
			"serie de nota de débito inválida %q: use FD## para aumentar facturas o BD## para aumentar boletas (ej. FD01, BD01)",
			code,
		)
	}
	return nil
}
