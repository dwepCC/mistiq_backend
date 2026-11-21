// Package receiptdesign define el diseño del PDF A4 de comprobantes que elige cada tenant.
//
// El diseño es solo presentación: se dibuja en el frontend (frontend_tenant/src/utils/receiptA4) a
// partir del mismo print_data. Aquí solo se guarda y se valida la clave (slug) elegida. El backend
// NO conoce la lista de diseños a propósito: así agregar un diseño nuevo no exige desplegar el
// backend. Un slug válido pero desconocido para el frontend cae al diseño por defecto allí.
package receiptdesign

import (
	"regexp"
	"strings"
)

// DefaultA4 es el diseño de todo tenant que no eligió otro (el vigente desde el 14-oct-2026).
const DefaultA4 = "modern"

var slugRe = regexp.MustCompile(`^[a-z0-9_]{1,30}$`)

// NormalizeA4 devuelve el slug en minúsculas si tiene forma válida; si no (vacío, símbolos,
// demasiado largo), el diseño por defecto.
func NormalizeA4(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if slugRe.MatchString(s) {
		return s
	}
	return DefaultA4
}
