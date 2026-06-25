package money

import "strings"

// CalcCheckoutDiscountAmount calcula el descuento global en moneda sobre la base imponible (subtotal).
// mode: "percent" (porcentaje 0–100) o "amount" (monto fijo en moneda).
func CalcCheckoutDiscountAmount(rawSubtotal float64, mode string, value float64) float64 {
	base := RoundSunat(max0(rawSubtotal))
	if base <= 0 {
		return 0
	}
	rawValue := max0(value)
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "percent", "percentage", "%":
		pct := rawValue
		if pct > 100 {
			pct = 100
		}
		return RoundSunat(base * (pct / 100))
	default:
		if rawValue > base {
			return RoundSunat(base)
		}
		return RoundSunat(rawValue)
	}
}

func max0(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}
