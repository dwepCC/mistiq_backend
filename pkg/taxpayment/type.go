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

// BnStatusSelfPending: estado de confirmación de una detracción con AUTODETRACCIÓN (el cliente paga el total
// al proveedor y este deposita la detracción en su propia cuenta BN). Vive en la misma columna que
// pending/confirmed/rejected (bn_confirmation_status, varchar(20)) para no requerir migración. En ese estado la
// venta se cobra por el TOTAL y no hay SPOT pendiente de confirmar. Solo afecta el cobro interno: el XML, el
// PDF fiscal y el facturador siguen recibiendo la detracción y las cuotas netas de siempre.
const BnStatusSelfPending = "self_pending"

// IsSelfDetractionStatus indica si el estado BN corresponde a una venta con autodetracción.
func IsSelfDetractionStatus(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), BnStatusSelfPending)
}
