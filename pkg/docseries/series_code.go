package docseries

import "strings"

// NormalizeSeriesCode normaliza el código de serie SUNAT (trim + mayúsculas).
func NormalizeSeriesCode(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}
