package money

import "strings"

// SalePaymentLine monto cobrado en una línea de pago (puede superar el total de la venta por vuelto).
type SalePaymentLine struct {
	ID     uint
	Amount float64
	// IsCash indica si esta línea es efectivo — únicamente el efectivo puede absorber vuelto
	// (ver AllocateSalePaymentReportAmounts). Calcularlo con IsCashMethod(method).
	IsCash bool
}

// IsCashMethod true si el código de método de pago representa efectivo. Mismo criterio simple
// usado como fallback en CashBankService.ResolveCashSessionForPayments y en
// tenantbackfills.v036IsCashMethod — no depende del catálogo TenantPaymentMethod (que refleja la
// configuración ACTUAL, no necesariamente la vigente cuando se registró un pago histórico).
func IsCashMethod(method string) bool {
	m := strings.ToLower(strings.TrimSpace(method))
	return m == "cash" || m == "efectivo"
}

// AllocateSalePaymentReportAmounts reparte el total cobrable de la venta entre las líneas de pago.
//
// Regla de negocio (vuelto): el vuelto SOLO puede salir de las líneas de EFECTIVO — nunca de un
// método electrónico (Yape/Plin/tarjeta/transferencia), porque ahí no existe una forma real de
// "devolver cambio". Si el cliente entregó más de lo debido, cada línea que no sea efectivo
// conserva su monto íntegro tal cual se ingresó; el excedente (vuelto) se resta únicamente de la
// línea (o líneas) de efectivo. Si hay más de una línea de efectivo, el vuelto se reparte
// proporcionalmente solo entre ellas.
//
// Dato histórico anómalo: si el vuelto supera el efectivo disponible (posible solo en ventas
// registradas ANTES de la validación que ahora bloquea esto en el momento de guardar — ver
// SaleService.Create), el efectivo se acota a 0 en vez de reportar un monto negativo; el
// excedente sin resolver queda, igual que antes, sin repartir a otras líneas.
func AllocateSalePaymentReportAmounts(saleTotal float64, payments []SalePaymentLine) map[uint]float64 {
	out := make(map[uint]float64, len(payments))
	if len(payments) == 0 {
		return out
	}
	payable := RoundDisplay(saleTotal)
	if payable < 0 {
		payable = 0
	}
	var sum, cashSum float64
	for _, p := range payments {
		amt := RoundDisplay(p.Amount)
		if amt <= 0 {
			continue
		}
		sum += amt
		if p.IsCash {
			cashSum += amt
		}
	}
	if sum <= payable+PaymentTolerance {
		for _, p := range payments {
			out[p.ID] = RoundDisplay(p.Amount)
		}
		return out
	}
	if sum <= 0 {
		return out
	}

	change := RoundDisplay(sum - payable)
	cashReportTotal := cashSum - change
	if cashReportTotal < 0 {
		cashReportTotal = 0
	}

	cashLines := 0
	for _, p := range payments {
		if p.IsCash && RoundDisplay(p.Amount) > 0 {
			cashLines++
		}
	}

	var allocatedCash float64
	seenCash := 0
	for _, p := range payments {
		amt := RoundDisplay(p.Amount)
		if amt <= 0 {
			out[p.ID] = 0
			continue
		}
		if !p.IsCash {
			out[p.ID] = amt
			continue
		}
		seenCash++
		var reportAmt float64
		if seenCash == cashLines {
			reportAmt = RoundDisplay(cashReportTotal - allocatedCash)
			if reportAmt < 0 {
				reportAmt = 0
			}
		} else {
			reportAmt = RoundDisplay(cashReportTotal * (amt / cashSum))
			allocatedCash += reportAmt
		}
		out[p.ID] = reportAmt
	}
	return out
}

// AllocateSalePaymentNetAmounts reparte el total cobrable entre las líneas de pago (antes de
// persistir IDs) preservando el orden — cada posición de `lines` corresponde a la misma posición
// del resultado.
func AllocateSalePaymentNetAmounts(saleTotal float64, lines []SalePaymentLine) []float64 {
	indexed := make([]SalePaymentLine, len(lines))
	for i, l := range lines {
		indexed[i] = SalePaymentLine{ID: uint(i), Amount: l.Amount, IsCash: l.IsCash}
	}
	byID := AllocateSalePaymentReportAmounts(saleTotal, indexed)
	out := make([]float64, len(lines))
	for i := range indexed {
		out[i] = byID[indexed[i].ID]
	}
	return out
}
