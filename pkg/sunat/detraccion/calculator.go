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
	// Legend2006TextTransporte: leyenda distinta para 1004 (mismo código de leyenda 2006, texto
	// propio — así lo hacía el sistema de referencia).
	Legend2006TextTransporte = "Operación Sujeta a Detracción - Servicios de Transporte de Carga"
	ThresholdGeneralPEN     = 700.0
)

// CalcInput parámetros para calcular detracción 1001 o 1004.
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
	// ValorReferencialPEN: solo 1004 (transporte de carga). Resolución 073-2006-SUNAT Art. 4: la
	// detracción se calcula sobre el mayor entre el importe de la operación y el valor
	// referencial (tablas MTC, D.S. 020-2021-MTC). Captura manual — igual que el sistema de
	// referencia, ninguno de los dos automatiza esas tablas.
	ValorReferencialPEN float64
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
	if op != OpDetraccionGeneral && op != OpDetraccionTransporte {
		return res, fmt.Errorf("tipo de operación %s no soportado para detracción; use %s o %s", op, OpDetraccionGeneral, OpDetraccionTransporte)
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
	// 027 (transporte de carga) es exclusivo de 1004 y viceversa — mismo criterio que el sistema
	// de referencia: mezclar el código con la operación equivocada produciría un comprobante que
	// declara un tipo de operación distinto del bien/servicio que en realidad se está cobrando.
	if op == OpDetraccionGeneral && good.TransportCargo {
		return res, fmt.Errorf("el código %s corresponde a transporte de carga; use operación %s", goodCode, OpDetraccionTransporte)
	}
	if op == OpDetraccionTransporte && !good.TransportCargo {
		return res, fmt.Errorf("la operación %s (transporte de carga) exige el código 027; %s no corresponde", OpDetraccionTransporte, goodCode)
	}
	if _, ok := cat.PaymentMethodByCode(res.PaymentMethodCode); !ok {
		return res, fmt.Errorf("medio de pago %s no válido en catálogo 59", res.PaymentMethodCode)
	}

	// Base de cálculo: 1001 usa el total gravado con IGV (comportamiento de siempre). 1004 usa el
	// importe TOTAL de la operación (incluido IGV) o el valor referencial, el que sea mayor —
	// Resolución 073-2006-SUNAT Art. 4. No es el mismo campo: el caso oficial de SUNAT (Lima-Casma,
	// S/2,000 incluido IGV) compara contra el importe COMPLETO, no contra la base sin IGV.
	var base float64
	var baseLabel string
	if op == OpDetraccionTransporte {
		base = money.RoundSunat(in.SaleTotalPEN)
		if vr := money.RoundSunat(in.ValorReferencialPEN); vr > base {
			base = vr
		}
		baseLabel = "el importe de la operación (o el valor referencial, si es mayor)"
	} else {
		base = money.RoundSunat(in.GravadoTotalPEN)
		baseLabel = "el importe gravado"
	}
	if base <= 0 {
		if op == OpDetraccionTransporte {
			return res, fmt.Errorf("la detracción por transporte de carga requiere un importe de operación o valor referencial mayor a cero")
		}
		return res, fmt.Errorf("la detracción requiere ítems gravados con IGV en la factura")
	}
	threshold := good.MinAmountPEN
	if threshold <= 0 {
		threshold = ThresholdGeneralPEN
	}
	if base <= threshold {
		return res, fmt.Errorf("%s (S/ %.2f) no supera el umbral mínimo de S/ %.2f para detracción", baseLabel, base, threshold)
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
