// Package sunatnote catálogos SUNAT de notas de crédito y débito.
//
// SUNAT exige que la representación impresa de una nota indique el tipo de nota (el código del
// motivo con su descripción) además del documento que modifica. Tener el catálogo en un solo
// sitio evita que el PDF, el listado y el XML digan cosas distintas.
package sunatnote

import "strings"

// creditReasons catálogo 09 — tipo de nota de crédito electrónica.
var creditReasons = map[string]string{
	"01": "Anulación de la operación",
	"02": "Anulación por error en el RUC",
	"03": "Corrección por error en la descripción",
	"04": "Descuento global",
	"05": "Descuento por ítem",
	"06": "Devolución total",
	"07": "Devolución por ítem",
	"08": "Bonificación",
	"09": "Disminución en el valor",
	"10": "Otros conceptos",
	"11": "Ajustes de operaciones de exportación",
	"12": "Ajustes afectos al IVAP",
	"13": "Ajustes - montos y/o fechas de pago",
}

// debitReasons catálogo 10 — tipo de nota de débito electrónica.
var debitReasons = map[string]string{
	"01": "Intereses por mora",
	"02": "Aumento en el valor",
	"03": "Penalidades / otros conceptos",
	"11": "Ajustes de operaciones de exportación",
	"12": "Ajustes afectos al IVAP",
}

// ReasonLabel descripción del motivo. tipoDoc "07" nota de crédito, "08" nota de débito.
// Devuelve cadena vacía si el código no está en el catálogo, para no inventar textos.
func ReasonLabel(tipoDoc, code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	table := creditReasons
	if strings.TrimSpace(tipoDoc) == "08" {
		table = debitReasons
	}
	return table[code]
}

// TypeLabel etiqueta lista para imprimir: «01 - Anulación de la operación».
// Si el código no está en el catálogo devuelve solo el código.
func TypeLabel(tipoDoc, code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	if label := ReasonLabel(tipoDoc, code); label != "" {
		return code + " - " + label
	}
	return code
}

// AffectedDocLabel nombre del tipo de comprobante afectado (catálogo 01).
func AffectedDocLabel(sunatCode string) string {
	switch strings.TrimSpace(sunatCode) {
	case "01":
		return "FACTURA"
	case "03":
		return "BOLETA DE VENTA"
	case "07":
		return "NOTA DE CRÉDITO"
	case "08":
		return "NOTA DE DÉBITO"
	case "12":
		return "TICKET"
	default:
		return ""
	}
}
