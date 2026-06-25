package detraccion

import (
	"fmt"
	"strings"

	"tukifac/pkg/money"
	"tukifac/pkg/salecurrency"
)

const (
	OpDetraccionGeneral     = "1001"
	OpDetraccionTransporte  = "1004"
	DefaultPaymentMethod    = "001"
	Legend2006Text          = "Operación sujeta a detracción"
	ThresholdGeneralPEN     = 700.0
)

// CalcInput parámetros para calcular detracción 1001.
type CalcInput struct {
	OperationTypeCode string
	SunatDocCode      string
	Currency          string
	ExchangeRate      *float64
	GravadoTotalPEN   float64
	SaleTotalPEN      float64
	GoodCode          string
	BankAccount       string
	PaymentMethodCode string
	ContactEsPercepcion bool
}

// CalcResult resultado del cálculo.
type CalcResult struct {
	Applicable        bool
	Reason            string
	GoodCode          string
	GoodLabel         string
	RatePercent       float64
	BaseAmountPEN     float64
	DetractionAmountPEN float64
	NetPayablePEN     float64
	BankAccount       string
	PaymentMethodCode string
}

// Evaluate calcula detracción para operación 1001.
func Evaluate(cat *CatalogProvider, in CalcInput) (CalcResult, error) {
	res := CalcResult{
		BankAccount:       strings.TrimSpace(in.BankAccount),
		PaymentMethodCode: strings.TrimSpace(in.PaymentMethodCode),
	}
	if res.PaymentMethodCode == "" {
		res.PaymentMethodCode = DefaultPaymentMethod
	}

	op := strings.TrimSpace(in.OperationTypeCode)
	if op == "" {
		op = salecurrency.OpVentaInterna
	}
	if op != OpDetraccionGeneral {
		return res, fmt.Errorf("tipo de operación %s no soportado para detracción en esta fase; use %s", op, OpDetraccionGeneral)
	}
	if in.SunatDocCode != "01" {
		return res, fmt.Errorf("la detracción solo aplica a facturas electrónicas (01)")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = salecurrency.CurrencyPEN
	}
	if currency != salecurrency.CurrencyPEN {
		return res, fmt.Errorf("la detracción requiere moneda PEN en la factura")
	}
	if in.ContactEsPercepcion {
		return res, fmt.Errorf("no se permite detracción con cliente agente de percepción")
	}
	if res.BankAccount == "" {
		return res, fmt.Errorf("configure la cuenta de detracción del Banco de la Nación en Configuración → SUNAT")
	}

	goodCode := strings.TrimSpace(in.GoodCode)
	if goodCode == "" {
		return res, fmt.Errorf("seleccione el bien o servicio sujeto a detracción (catálogo 54)")
	}
	if cat == nil {
		return res, fmt.Errorf("catálogo de detracción no disponible")
	}
	good, ok := cat.GoodByCode(goodCode)
	if !ok {
		return res, fmt.Errorf("código de bien/servicio %s no válido en catálogo 54", goodCode)
	}
	if good.TransportCargo {
		return res, fmt.Errorf("el código %s corresponde a transporte de carga; use operación 1004 (no habilitada)", goodCode)
	}
	if _, ok := cat.PaymentMethodByCode(res.PaymentMethodCode); !ok {
		return res, fmt.Errorf("medio de pago %s no válido en catálogo 59", res.PaymentMethodCode)
	}

	base := money.RoundSunat(in.GravadoTotalPEN)
	if base <= 0 {
		return res, fmt.Errorf("la detracción requiere ítems gravados con IGV en la factura")
	}
	threshold := good.MinAmountPEN
	if threshold <= 0 {
		threshold = ThresholdGeneralPEN
	}
	if base <= threshold {
		return res, fmt.Errorf("el importe gravado (S/ %.2f) no supera el umbral mínimo de S/ %.2f para detracción", base, threshold)
	}

	rate := good.RatePercent
	amount := money.RoundSunat(base * rate / 100)
	saleTotal := money.RoundSunat(in.SaleTotalPEN)
	if saleTotal <= 0 {
		saleTotal = base
	}
	net := money.RoundSunat(saleTotal - amount)
	if net < 0 {
		net = 0
	}

	res.Applicable = true
	res.Reason = fmt.Sprintf("Detracción del %.2f%% sobre operaciones gravadas", rate)
	res.GoodCode = good.Code
	res.GoodLabel = good.Description
	res.RatePercent = rate
	res.BaseAmountPEN = base
	res.DetractionAmountPEN = amount
	res.NetPayablePEN = net
	res.BankAccount = res.BankAccount
	return res, nil
}

// GravadoTotalFromItems suma totales de líneas gravadas (cat. 07 = 10).
func GravadoTotalFromItems(items []ItemAffectation) float64 {
	var total float64
	for _, it := range items {
		if strings.TrimSpace(it.IgvAffectationType) == "10" {
			total += it.Total
		}
	}
	return money.RoundSunat(total)
}

// ItemAffectation línea mínima para cálculo de base gravada.
type ItemAffectation struct {
	IgvAffectationType string
	Total              float64
}
