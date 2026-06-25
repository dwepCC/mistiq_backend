package service

import (
	"fmt"
	"strings"

	"tukifac/pkg/taxpayment"

	"gorm.io/gorm"
)

// normalizeReportMethod unifica códigos para reportes/UI (cash ↔ efectivo).
func normalizeReportMethod(m string) string {
	m = strings.TrimSpace(strings.ToLower(m))
	switch NormalizePaymentMethodCode(m) {
	case "cash":
		return "efectivo"
	case "yape":
		return "yape"
	case "plin":
		return "plin"
	case "tarjeta":
		return "tarjeta"
	case "transferencia":
		return "transferencia"
	case "detraccion_bn":
		return "detraccion_bn"
	default:
		if m == "" {
			return "efectivo"
		}
		return m
	}
}

// paymentMethodVariantsLower devuelve variantes en minúsculas para filtros SQL.
func paymentMethodVariantsLower(code string) []string {
	norm := normalizeReportMethod(code)
	switch norm {
	case "efectivo":
		return []string{"cash", "efectivo", ""}
	case "yape":
		return []string{"yape"}
	case "plin":
		return []string{"plin"}
	case "tarjeta":
		return []string{"tarjeta", "card"}
	case "transferencia":
		return []string{"transferencia", "transfer"}
	default:
		c := strings.TrimSpace(strings.ToLower(code))
		if c == "" {
			return []string{"cash", "efectivo", ""}
		}
		return []string{c}
	}
}

func applyPaymentMethodFilter(q *gorm.DB, column, paymentMethod string) *gorm.DB {
	if paymentMethod == "" {
		return q
	}
	variants := paymentMethodVariantsLower(paymentMethod)
	return q.Where(fmt.Sprintf("LOWER(TRIM(%s)) IN ?", column), variants)
}

func salePaymentMovementID(paymentID uint) uint {
	return 1_000_000_000 + paymentID
}

// IsDetractionPaymentMethod indica método interno SPOT (sin impacto en caja/banco).
func IsDetractionPaymentMethod(method string) bool {
	return taxpayment.IsDetractionCode(method)
}

// IsCashPaymentMethod indica si el método representa dinero físico en caja.
func IsCashPaymentMethod(method string) bool {
	return normalizeReportMethod(method) == "efectivo"
}

// movementRowChannel clasifica una fila de movimientos: "cash", "electronic" o "detraction".
func movementRowChannel(row MovementReportRow) string {
	if row.Type == "venta" && IsDetractionPaymentMethod(row.PaymentMethod) {
		return "detraction"
	}
	if row.Type == "venta" && !IsCashPaymentMethod(row.PaymentMethod) {
		return "electronic"
	}
	return "cash"
}
