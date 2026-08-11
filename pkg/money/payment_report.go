package money

// SalePaymentLine monto cobrado en una línea de pago (puede superar el total de la venta por vuelto).
type SalePaymentLine struct {
	ID     uint
	Amount float64
}

// AllocateSalePaymentReportAmounts reparte el total cobrable de la venta entre las líneas de pago.
// Si el cliente entregó más (vuelto), cada línea refleja su parte del importe de venta, no el bruto recibido.
func AllocateSalePaymentReportAmounts(saleTotal float64, payments []SalePaymentLine) map[uint]float64 {
	out := make(map[uint]float64, len(payments))
	if len(payments) == 0 {
		return out
	}
	payable := RoundDisplay(saleTotal)
	if payable < 0 {
		payable = 0
	}
	var sum float64
	for _, p := range payments {
		amt := RoundDisplay(p.Amount)
		if amt > 0 {
			sum += amt
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
	var allocated float64
	for i, p := range payments {
		amt := RoundDisplay(p.Amount)
		if amt <= 0 {
			out[p.ID] = 0
			continue
		}
		var reportAmt float64
		if i == len(payments)-1 {
			reportAmt = RoundDisplay(payable - allocated)
			if reportAmt < 0 {
				reportAmt = 0
			}
		} else {
			reportAmt = RoundDisplay(payable * (amt / sum))
			allocated += reportAmt
		}
		out[p.ID] = reportAmt
	}
	return out
}

// AllocateSalePaymentNetAmounts reparte el total cobrable entre índices de pago (antes de persistir IDs).
func AllocateSalePaymentNetAmounts(saleTotal float64, amounts []float64) []float64 {
	lines := make([]SalePaymentLine, len(amounts))
	for i, amt := range amounts {
		lines[i] = SalePaymentLine{ID: uint(i), Amount: amt}
	}
	byID := AllocateSalePaymentReportAmounts(saleTotal, lines)
	out := make([]float64, len(amounts))
	for i := range amounts {
		out[i] = byID[uint(i)]
	}
	return out
}
