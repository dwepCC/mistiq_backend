// Package money centraliza redondeo de montos: 6 decimales (cálculo/BD/SUNAT) y 2 (visualización/pagos en efectivo).
package money

import "math"

const (
	SunatDecimals   = 6
	DisplayDecimals = 2
	// PaymentTolerance compara cobros ingresados (2 decimales) vs total calculado.
	PaymentTolerance = 0.01
)

// RoundSunat redondea a 6 decimales (precisión interna y persistencia).
func RoundSunat(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// RoundDisplay redondea a 2 decimales (montos mostrados al usuario y comparación de pagos).
func RoundDisplay(v float64) float64 {
	return math.Round(v*100) / 100
}

// PaidCoversTotal indica si el monto pagado cubre el total esperado (tolerancia de céntimos).
func PaidCoversTotal(paid, expected float64) bool {
	return RoundDisplay(paid)+PaymentTolerance >= RoundDisplay(expected)
}

// CalcPaymentChange devuelve el vuelto cuando el cliente entrega más del monto cobrable.
func CalcPaymentChange(paid, payable float64) float64 {
	diff := RoundDisplay(paid) - RoundDisplay(payable)
	if diff <= PaymentTolerance {
		return 0
	}
	return RoundDisplay(diff)
}
