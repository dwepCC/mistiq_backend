package service

import (
	"errors"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/datespe"
	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
)

// notaCreditPlan es lo que se traslada de una NV a crédito a la factura/boleta que la reemplaza.
type notaCreditPlan struct {
	Installments []CreditInstallmentInput
	LastDue      time.Time
}

// isCreditNota indica si la NV nació a crédito (condición, estado o cuotas registradas).
func isCreditNota(nota *database.TenantSale, installments []database.TenantSaleCreditInstallment) bool {
	return paymentcondition.IsCreditCode(nota.PaymentConditionCode) || nota.Status == "credit" || len(installments) > 0
}

// buildNotaCreditPlan arma las cuotas de la factura/boleta a partir del saldo PENDIENTE de la NV.
//
// Decisión (opción A): el comprobante electrónico hereda la deuda y la NV queda como historial
// (se excluye de Cuentas por Cobrar mientras tenga un comprobante vigente, ver
// salescope.ExcludeNotaWithLiveIssued). Los abonos reales ya hechos sobre la NV se copian como
// pagos del nuevo documento SIN tocar caja/bancos (ya entraron al cobrarse la NV), así la factura
// nace con el mismo saldo. Solo se trasladan los saldos pendientes de cada cuota
// (amount − paid_amount). Fechas que ya no son posteriores a la emisión (SUNAT 3267) se mueven al
// día siguiente a la emisión. Devuelve (nil, nil) si la NV ya está saldada (se emite como contado).
func buildNotaCreditPlan(nota *database.TenantSale, insts []database.TenantSaleCreditInstallment, realPays []PaymentInput, issueAt time.Time) (*notaCreditPlan, error) {
	remaining := money.RoundDisplay(nota.Total - sumDirectPaymentsExclSpecial(realPays))
	if remaining <= 0.009 {
		return nil, nil
	}
	loc := datespe.Location()
	issueDay := time.Date(issueAt.In(loc).Year(), issueAt.In(loc).Month(), issueAt.In(loc).Day(), 0, 0, 0, 0, loc)
	minDue := issueDay.AddDate(0, 0, 1)
	clamp := func(d time.Time) string {
		dl := d.In(loc)
		day := time.Date(dl.Year(), dl.Month(), dl.Day(), 0, 0, 0, 0, loc)
		if day.Before(minDue) {
			day = minDue
		}
		return day.Format("2006-01-02")
	}

	var out []CreditInstallmentInput
	var sum float64
	for _, in := range insts {
		pend := money.RoundDisplay(in.Amount - in.PaidAmount)
		if pend <= 0.009 {
			continue
		}
		out = append(out, CreditInstallmentInput{DueDate: clamp(in.DueDate), Amount: pend})
		sum += pend
	}
	if len(out) == 0 {
		if nota.DueDate == nil {
			return nil, errors.New("la nota de venta a crédito no tiene cuotas ni fecha de vencimiento para trasladar al comprobante")
		}
		out = []CreditInstallmentInput{{DueDate: clamp(*nota.DueDate), Amount: remaining}}
		sum = remaining
	}
	// Cuadre: el saldo trasladado debe igualar total − pagos reales. La diferencia (redondeos o
	// cuotas desactualizadas) se absorbe en la última cuota.
	if diff := money.RoundDisplay(remaining - sum); diff != 0 {
		last := &out[len(out)-1]
		adj := money.RoundDisplay(last.Amount + diff)
		if adj <= 0.009 {
			return nil, errors.New("las cuotas pendientes de la nota de venta no cuadran con su saldo; revise la nota antes de convertirla")
		}
		last.Amount = adj
	}
	due, err := time.ParseInLocation("2006-01-02", out[len(out)-1].DueDate, loc)
	if err != nil {
		return nil, err
	}
	return &notaCreditPlan{Installments: out, LastDue: due}, nil
}
