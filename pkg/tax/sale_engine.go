package tax

import "tukifac/pkg/money"

// Catálogo SUNAT N°53 — descuentos comerciales UBL 2.1 / Greenter.
const (
	AllowanceCodeLineDiscountAffectsIGV   = "00" // ítem, afecta base IGV
	AllowanceCodeGlobalDiscountAffectsIGV = "02" // documento, afecta base IGV
)

// SaleLineInput entrada de una línea para el motor unificado de ventas.
type SaleLineInput struct {
	UnitPrice          float64
	Quantity           float64
	IgvAffectationType string
	PriceIncludesIgv   bool
	// Descuento por línea sobre base imponible (subtotal bruto de la línea).
	LineDiscountMode  string
	LineDiscountValue float64
}

// SaleCheckoutInput entrada completa de una venta con descuentos por línea y global.
type SaleCheckoutInput struct {
	Lines              []SaleLineInput
	GlobalDiscountMode string
	GlobalDiscountValue float64
	TaxCfg             Config
}

// AllowanceCharge representa un descuento/cargo UBL 2.1 (Greenter Charge).
type AllowanceCharge struct {
	CodTipo   string  `json:"codTipo"`
	Factor    float64 `json:"factor,omitempty"`
	Monto     float64 `json:"monto"`
	MontoBase float64 `json:"montoBase"`
}

// SaleLineResult resultado tributario por línea.
type SaleLineResult struct {
	GrossSubtotal          float64 // base antes de cualquier descuento
	LineDiscountSubtotal   float64
	SubtotalAfterLine      float64 // mtoValorVenta UBL (antes de descuento global)
	GlobalDiscountSubtotal float64
	Subtotal               float64 // base imponible final
	TaxAmount              float64
	Total                  float64
	TaxRate                float64
	StoredDiscount         float64 // TenantSaleItem.Discount (formato CalcItem)
	LineCharges            []AllowanceCharge
}

// SaleResult resultado agregado de la venta.
type SaleResult struct {
	Lines                []SaleLineResult
	Subtotal             float64
	TaxAmount            float64
	Total                float64
	GlobalDiscountAmount float64
	GlobalCharges        []AllowanceCharge
	SumOtrosDescuentos   float64
}

// CalcSaleCheckout calcula una venta completa.
//
// Orden normativo (SUNAT Cat. 53 + ejemplos Greenter):
//  1. Subtotal bruto por línea (qty × precio, descomponiendo IGV si aplica).
//  2. Descuento por línea (cod 00) sobre la base de esa línea.
//  3. Descuento global (cod 02) sobre la suma de bases netas post-descuento línea.
//  4. Reparto proporcional del global entre líneas.
//  5. IGV y demás tributos sobre la base final de cada línea.
func CalcSaleCheckout(in SaleCheckoutInput) SaleResult {
	taxCfg := in.TaxCfg
	n := len(in.Lines)
	out := SaleResult{Lines: make([]SaleLineResult, n)}

	if n == 0 {
		return out
	}

	afterLineSubs := make([]float64, n)
	var subtotalAfterLineSum float64
	for i, line := range in.Lines {
		aff := normalizeAff(line.IgvAffectationType)
		grossSub, _, _ := CalcItem(line.UnitPrice, line.Quantity, 0, aff, line.PriceIncludesIgv, taxCfg)
		grossSub = money.RoundSunat(grossSub)
		lineDisc := money.CalcCheckoutDiscountAmount(grossSub, line.LineDiscountMode, line.LineDiscountValue)
		afterLine, _, _ := CalcItemWithSubtotalDiscount(
			line.UnitPrice, line.Quantity, lineDisc, aff, line.PriceIncludesIgv, taxCfg,
		)
		afterLine = money.RoundSunat(afterLine)

		var lineCharges []AllowanceCharge
		if lineDisc > 0 {
			lineCharges = []AllowanceCharge{buildAllowance(AllowanceCodeLineDiscountAffectsIGV, grossSub, lineDisc)}
		}

		out.Lines[i] = SaleLineResult{
			GrossSubtotal:        grossSub,
			LineDiscountSubtotal: lineDisc,
			SubtotalAfterLine:    afterLine,
			LineCharges:          lineCharges,
			TaxRate:              taxCfg.EffectiveRate(aff),
		}
		afterLineSubs[i] = afterLine
		if !IsBonificacionGravada(aff) {
			subtotalAfterLineSum = money.RoundSunat(subtotalAfterLineSum + afterLine)
		}
	}

	globalDisc := money.CalcCheckoutDiscountAmount(subtotalAfterLineSum, in.GlobalDiscountMode, in.GlobalDiscountValue)
	chargeableAfterLine := make([]float64, n)
	for i, line := range in.Lines {
		aff := normalizeAff(line.IgvAffectationType)
		if IsBonificacionGravada(aff) {
			chargeableAfterLine[i] = 0
		} else {
			chargeableAfterLine[i] = afterLineSubs[i]
		}
	}
	globalShares := money.DistributeCheckoutDiscountToLines(chargeableAfterLine, globalDisc)

	var globalCharges []AllowanceCharge
	if globalDisc > 0 {
		globalCharges = []AllowanceCharge{buildAllowance(AllowanceCodeGlobalDiscountAffectsIGV, subtotalAfterLineSum, globalDisc)}
	}

	for i, line := range in.Lines {
		aff := normalizeAff(line.IgvAffectationType)
		globalShare := globalShares[i]
		finalSub := money.RoundSunat(max0(out.Lines[i].SubtotalAfterLine - globalShare))
		rate := taxCfg.EffectiveRate(aff)

		var taxAmt, total float64
		if rate == 0 {
			taxAmt = 0
			total = finalSub
		} else {
			taxAmt = money.RoundSunat(finalSub * (rate / 100))
			total = money.RoundSunat(finalSub + taxAmt)
		}

		totalDiscSub := money.RoundSunat(out.Lines[i].LineDiscountSubtotal + globalShare)
		storedDisc := SubtotalDiscountToLineDiscount(
			line.UnitPrice, line.Quantity, totalDiscSub, aff, line.PriceIncludesIgv, taxCfg,
		)

		lr := out.Lines[i]
		lr.GlobalDiscountSubtotal = globalShare
		lr.Subtotal = finalSub
		lr.TaxAmount = taxAmt
		if IsBonificacionGravada(aff) {
			lr.Total = 0
		} else {
			lr.Total = total
			out.Subtotal = money.RoundSunat(out.Subtotal + finalSub)
			out.TaxAmount = money.RoundSunat(out.TaxAmount + taxAmt)
			out.Total = money.RoundSunat(out.Total + total)
		}
		lr.StoredDiscount = storedDisc
		out.Lines[i] = lr
	}

	out.GlobalDiscountAmount = globalDisc
	out.GlobalCharges = globalCharges
	out.SumOtrosDescuentos = globalDisc
	return out
}

func buildAllowance(codTipo string, montoBase, monto float64) AllowanceCharge {
	montoBase = money.RoundSunat(max0(montoBase))
	monto = money.RoundSunat(max0(monto))
	factor := 0.0
	if montoBase > 0 {
		// Greenter/SUNAT: factor decimal (ej. 0.10 = 10%).
		factor = monto / montoBase
	}
	return AllowanceCharge{
		CodTipo:   codTipo,
		Factor:    factor,
		Monto:     monto,
		MontoBase: montoBase,
	}
}

func normalizeAff(code string) string {
	if code == "" {
		return "10"
	}
	return code
}
