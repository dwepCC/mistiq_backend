package detraccion

import "tukifac/pkg/money"

// NetInstallmentAmounts convierte las cuotas de cobro de una venta con AUTODETRACCIÓN (que incluyen la
// detracción: suman saldo a crédito + detracción) en las cuotas FISCALES que SUNAT espera (que suman el neto
// pendiente = lo anterior − detracción). Cada cuota se reduce en la misma proporción, así con cuotas iguales
// el resultado es idéntico al de una venta normal con detracción; el redondeo sobrante se absorbe en la última
// cuota para que la suma sea exactamente la meta. Si no hay nada que ajustar (sin cuotas, detracción <= 0 o
// mayor/igual a la suma) devuelve las cuotas tal cual.
func NetInstallmentAmounts(amounts []float64, detraction float64) []float64 {
	out := append([]float64(nil), amounts...)
	if len(out) == 0 || detraction <= 0 {
		return out
	}
	var total float64
	for _, a := range out {
		total += a
	}
	target := money.RoundDisplay(total - detraction)
	if total <= 0 || target <= 0 {
		return out
	}
	factor := target / total
	var sum float64
	for i := range out {
		if i == len(out)-1 {
			out[i] = money.RoundDisplay(target - sum)
			break
		}
		out[i] = money.RoundDisplay(out[i] * factor)
		sum += out[i]
	}
	return out
}
