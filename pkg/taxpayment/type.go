package taxpayment

import "strings"

const (
	CodeDetraccionBN = "detraccion_bn"
	NameDetraccionBN = "Detracción BN (SPOT)"
)

// IsDetractionCode indica línea SPOT en pagos de venta (dominio tributario).
func IsDetractionCode(code string) bool {
	return strings.EqualFold(strings.TrimSpace(code), CodeDetraccionBN)
}
