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

// Serie SUNAT de factura/boleta electrónica: letra correspondiente al tipo (F o B) + 3
// caracteres alfanuméricos (4 en total, ej. F001, B001, BB08). Antes no se validaba nada acá:
// un tenant pudo guardar "005" como serie de boleta, formato que SUNAT rechaza al momento de
// emitir (el PSE valida el nombre del ZIP RUC-tipoDoc-serie-numero antes de validar el
// documento, y "005" no matchea el patrón esperado) — el rechazo llega recién al emitir, no al
// guardar la serie, así que pasa desapercibido hasta que ya hay comprobantes fallidos.
var facturaSeriesRE = regexp.MustCompile(`^F[A-Z0-9]{3}$`)
var boletaSeriesRE = regexp.MustCompile(`^B[A-Z0-9]{3}$`)

// ValidateFacturaBoletaSeriesCode valida formato F### / B### al crear o editar series de
// factura/boleta electrónica. documentCode: "01" factura, "03" boleta.
func ValidateFacturaBoletaSeriesCode(documentCode, seriesName string) error {
	code := NormalizeSeriesCode(seriesName)
	if code == "" {
		return fmt.Errorf("serie requerida")
	}
	if documentCode == "01" {
		if !facturaSeriesRE.MatchString(code) {
			return fmt.Errorf(
				"serie de factura inválida %q: debe empezar con F y tener 4 caracteres (ej. F001)",
				code,
			)
		}
		return nil
	}
	if documentCode == "03" {
		if !boletaSeriesRE.MatchString(code) {
			return fmt.Errorf(
				"serie de boleta inválida %q: debe empezar con B y tener 4 caracteres (ej. B001)",
				code,
			)
		}
		return nil
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
	if def.Category == "nota_debito" {
		return ValidateNotaDebitoSeriesCode(seriesName)
	}
	if def.Category == "venta" && (def.DocumentCode == "01" || def.DocumentCode == "03") {
		return ValidateFacturaBoletaSeriesCode(def.DocumentCode, seriesName)
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
