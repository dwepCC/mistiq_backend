package service

import (
	"errors"
	"fmt"

	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
	sunatdet "tukifac/pkg/sunat/detraccion"
	"tukifac/pkg/taxpayment"
)

// PrepareDetractionSalePayments normaliza pagos directos contra net_payable_pen y añade la línea detraccion_bn (contado).
func PrepareDetractionSalePayments(
	payments []PaymentInput,
	saleTotal float64,
	eval sunatdet.CalcResult,
) ([]PaymentInput, error) {
	out, _, err := finalizeDetractionSalePayments(payments, saleTotal, eval, false)
	return out, err
}

// PrepareDetractionSalePaymentsAllowCredit igual que PrepareDetractionSalePayments pero permite saldo a crédito (P3).
func PrepareDetractionSalePaymentsAllowCredit(
	payments []PaymentInput,
	saleTotal float64,
	eval sunatdet.CalcResult,
) ([]PaymentInput, bool, error) {
	return finalizeDetractionSalePayments(payments, saleTotal, eval, true)
}

func finalizeDetractionSalePayments(
	payments []PaymentInput,
	saleTotal float64,
	eval sunatdet.CalcResult,
	allowCredit bool,
) ([]PaymentInput, bool, error) {
	if !eval.Applicable {
		return nil, false, errors.New("la detracción no aplica para esta venta")
	}
	netPayable := eval.NetPayablePEN
	detractionAmount := eval.DetractionAmountPEN

	direct := make([]PaymentInput, 0, len(payments))
	for _, p := range payments {
		if taxpayment.IsDetractionCode(p.Method) || paymentcondition.IsCreditCode(p.Method) {
			continue
		}
		if p.Amount <= 0 || p.Method == "" {
			continue
		}
		direct = append(direct, p)
	}

	var sumDirect float64
	for _, p := range direct {
		sumDirect += p.Amount
	}
	if !allowCredit && !money.PaidCoversTotal(sumDirect, netPayable) {
		return nil, false, fmt.Errorf(
			"la suma de pagos directos (S/ %.2f) no cubre el neto cobrable (S/ %.2f)",
			money.RoundDisplay(sumDirect), money.RoundDisplay(netPayable),
		)
	}
	if money.RoundDisplay(sumDirect) >= money.RoundDisplay(saleTotal)-money.PaymentTolerance {
		return nil, false, fmt.Errorf(
			"el pago directo (S/ %.2f) no puede cubrir el total de la factura (S/ %.2f); indique solo el neto cobrable",
			money.RoundDisplay(sumDirect), money.RoundDisplay(saleTotal),
		)
	}
	if detractionAmount <= 0 {
		return nil, false, errors.New("el monto de detracción debe ser mayor a cero")
	}

	isCredit := allowCredit && !money.PaidCoversTotal(sumDirect, netPayable)

	out := append([]PaymentInput{}, direct...)
	out = append(out, PaymentInput{
		Method: taxpayment.CodeDetraccionBN,
		Amount: detractionAmount,
	})

	var sumAll float64
	for _, p := range out {
		sumAll += p.Amount
	}
	directOver := 0.0
	if money.RoundDisplay(sumDirect) > money.RoundDisplay(netPayable)+money.PaymentTolerance {
		directOver = sumDirect - netPayable
	}
	if money.RoundDisplay(sumAll) > money.RoundDisplay(saleTotal)+money.RoundDisplay(directOver)+money.PaymentTolerance {
		return nil, false, fmt.Errorf(
			"la suma de pagos (S/ %.2f) supera el total de la factura (S/ %.2f)",
			money.RoundDisplay(sumAll), money.RoundDisplay(saleTotal),
		)
	}
	if !isCredit && !money.PaidCoversTotal(sumAll, saleTotal) {
		return nil, false, fmt.Errorf(
			"la suma de pagos (S/ %.2f) no coincide con el total de la factura (S/ %.2f)",
			money.RoundDisplay(sumAll), money.RoundDisplay(saleTotal),
		)
	}
	return out, isCredit, nil
}

// IsCreditSaleFromPayments indica venta a crédito según pagos directos vs total (sin detracción).
func IsCreditSaleFromPayments(payments []PaymentInput, total float64) bool {
	var sumDirect float64
	for _, p := range payments {
		if taxpayment.IsDetractionCode(p.Method) || paymentcondition.IsCreditCode(p.Method) {
			continue
		}
		if p.Amount <= 0 {
			continue
		}
		sumDirect += p.Amount
	}
	return total > 0 && !money.PaidCoversTotal(sumDirect, total)
}

// PrimaryDirectPaymentMethod devuelve el primer método de pago directo (excluye detraccion_bn).
func PrimaryDirectPaymentMethod(payments []PaymentInput, fallback string) string {
	for _, p := range payments {
		if p.Amount <= 0 || p.Method == "" {
			continue
		}
		if taxpayment.IsDetractionCode(p.Method) {
			continue
		}
		if paymentcondition.IsCreditCode(p.Method) {
			continue
		}
		return p.Method
	}
	return fallback
}
