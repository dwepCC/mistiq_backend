package service

import (
	salecontext "tukifac/internal/fiscal/salecontext"
	"tukifac/pkg/money"
)

// igvRetentionAmountForPayments devuelve la retención IGV que el cliente (agente de retención) descuenta
// del pago de la factura, o 0 si no aplica. Usa el mismo motor que salecontext.Persist, de modo que lo
// que se exige cobrar coincide con el "neto a cobrar" que se guarda y se imprime. El cliente deposita la
// retención a SUNAT, así que los pagos directos solo deben cubrir total − retención.
func igvRetentionAmountForPayments(
	fc *salecontext.FiscalContextInput,
	sunatCode string,
	contact *salecontext.ContactSnapshot,
	total float64,
	currency string,
	exchangeRate *float64,
) float64 {
	if fc == nil || salecontext.IsEmptyFiscalInput(fc) {
		return 0
	}
	requested, manual := salecontext.ResolveRequestedRetention(fc, sunatCode, contact, total, currency, exchangeRate)
	eval := salecontext.EvaluateIGVRetention(salecontext.RetentionEvalInput{
		RequestedRetention: requested,
		ManualOverride:     manual,
		SunatDocCode:       sunatCode,
		Contact:            contact,
		SaleTotal:          total,
		Currency:           currency,
		ExchangeRate:       exchangeRate,
	})
	if !eval.HasIgvRetention || !eval.Applicable || eval.ObligationAmount <= 0 {
		return 0
	}
	return money.RoundSunat(eval.ObligationAmount)
}
