package facturador

import (
	"regexp"
	"strings"
)

var (
	freeTextLineBreakPattern = regexp.MustCompile(`[\r\n\t]+`)
	freeTextRepeatedSpace    = regexp.MustCompile(` {2,}`)
)

// SanitizeFreeText normaliza texto libre que termina en un nodo Note/leyenda del
// XML UBL (p. ej. Observacion de facturas/retenciones/percepciones, DesMotivo de
// notas de crédito/débito, DesMotivoBaja de comunicaciones de baja): SUNAT
// rechaza (código 3006, "descripcion de leyenda no cumple con el formato
// establecido") cualquier salto de línea u otro carácter de control dentro de
// esos nodos — típicamente porque el usuario tecleó un Enter en el campo.
//
// Reemplaza \r\n/\r/\n/\t por espacio, colapsa espacios repetidos y recorta
// los extremos. Aplicar siempre que un campo de texto libre del usuario vaya a
// terminar en uno de esos nodos, antes de mandarlo a Lycet.
//
// Caso real: tenant aarservicios (RUC 20548414424), F001-78/79/80 rechazadas
// el 2026-09-02 por un \n dentro de "observacion".
func SanitizeFreeText(s string) string {
	clean := freeTextLineBreakPattern.ReplaceAllString(s, " ")
	clean = freeTextRepeatedSpace.ReplaceAllString(clean, " ")
	return strings.TrimSpace(clean)
}
