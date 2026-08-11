package prepayment

import (
	"fmt"
	"math"
)

const (
	DiscountCodeGravadoAnticipo   = "04"
	DiscountCodeExoneradoAnticipo = "05"
	DiscountCodeInafectoAnticipo  = "06"
)

func roundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}

func roundFactor(v float64) float64 {
	return math.Round(v*100000) / 100000
}

// DiscountCodeForGroup devuelve el código SUNAT de descuento global por anticipo (04/05/06).
func DiscountCodeForGroup(group string) (string, error) {
	switch group {
	case AffectationGravado:
		return DiscountCodeGravadoAnticipo, nil
	case AffectationExonerado:
		return DiscountCodeExoneradoAnticipo, nil
	case AffectationInafecto:
		return DiscountCodeInafectoAnticipo, nil
	default:
		return "", fmt.Errorf("grupo de afectación no válido para deducción: %s", group)
	}
}

// DeductionTotalFromBase calcula el monto total deducido (con IGV si gravado).
func DeductionTotalFromBase(baseAmount float64, group string, taxRatePercent float64) float64 {
	if group == AffectationGravado && taxRatePercent > 0 {
		return roundMoney(baseAmount * (1 + taxRatePercent/100))
	}
	return roundMoney(baseAmount)
}

// DeductionTotalFromBaseCapped como DeductionTotalFromBase pero sin superar el saldo del voucher.
func DeductionTotalFromBaseCapped(baseAmount float64, group string, taxRatePercent float64, balanceCap float64) float64 {
	total := DeductionTotalFromBase(baseAmount, group, taxRatePercent)
	if balanceCap > 0 && total > balanceCap {
		return roundMoney(balanceCap)
	}
	return total
}

// SaleGroupDeductibleBase devuelve la base máxima deducible de la venta según afectación.
func SaleGroupDeductibleBase(group string, totals SaleGroupTotals) float64 {
	switch group {
	case AffectationGravado:
		return totals.GravadoSubtotal
	case AffectationExonerado:
		return totals.ExoneradoSubtotal
	case AffectationInafecto:
		return totals.InafectoSubtotal
	default:
		return 0
	}
}

// BaseFromBalance separa saldo del voucher en base imponible y total (legacy PHP).
func BaseFromBalance(balanceAmount float64, group string, taxRatePercent float64) (base, total float64) {
	total = roundMoney(balanceAmount)
	if group == AffectationGravado && taxRatePercent > 0 {
		return roundMoney(balanceAmount / (1 + taxRatePercent/100)), total
	}
	return total, total
}

// SaleGroupTotals totales de venta por grupo IGV antes de deducir anticipos.
type SaleGroupTotals struct {
	GravadoSubtotal   float64
	GravadoTax        float64
	GravadoTotal      float64
	ExoneradoSubtotal float64
	ExoneradoTotal    float64
	InafectoSubtotal  float64
	InafectoTotal     float64
	Subtotal          float64
	TaxAmount         float64
	Total             float64
}

// ApplyDeductionResult resultado de aplicar deducciones de anticipo a totales de venta.
type ApplyDeductionResult struct {
	Totals           SaleGroupTotals
	DeductionBase    float64
	DeductionTotal   float64
	DiscountCode     string
	DiscountFactor   float64
	DiscountBase     float64
	DiscountAmount   float64
	DiscountDesc     string
}

func discountDescription(code string) string {
	switch code {
	case DiscountCodeGravadoAnticipo:
		return "Descuentos globales por anticipos gravados que afectan la base imponible del IGV/IVAP"
	case DiscountCodeExoneradoAnticipo:
		return "Descuentos globales por anticipos exonerados"
	case DiscountCodeInafectoAnticipo:
		return "Descuentos globales por anticipos inafectos"
	default:
		return "Descuento por anticipos"
	}
}

// ApplyDeductionToSaleTotals ajusta totales como invoice_generate.vue (discountGlobalPrepayment).
func ApplyDeductionToSaleTotals(
	group string,
	totals SaleGroupTotals,
	deductionBaseSum float64,
	deductionTotalSum float64,
	taxRatePercent float64,
) (ApplyDeductionResult, error) {
	code, err := DiscountCodeForGroup(group)
	if err != nil {
		return ApplyDeductionResult{}, err
	}
	if deductionBaseSum <= 0 || deductionTotalSum <= 0 {
		return ApplyDeductionResult{}, fmt.Errorf("indique montos de anticipo a deducir")
	}
	amount := roundMoney(deductionBaseSum)
	out := ApplyDeductionResult{
		Totals:         totals,
		DeductionBase:  amount,
		DeductionTotal: roundMoney(deductionTotalSum),
		DiscountCode:   code,
		DiscountAmount: amount,
		DiscountDesc:   discountDescription(code),
	}
	switch group {
	case AffectationGravado:
		base := roundMoney(totals.GravadoSubtotal + amount)
		if base <= 0 {
			return ApplyDeductionResult{}, fmt.Errorf("base gravada inválida para deducir anticipo")
		}
		out.DiscountBase = base
		out.DiscountFactor = roundFactor(amount / base)
		out.Totals.GravadoSubtotal = roundMoney(totals.GravadoSubtotal - amount)
		out.Totals.GravadoTax = roundMoney(out.Totals.GravadoSubtotal * (taxRatePercent / 100))
		out.Totals.GravadoTotal = roundMoney(out.Totals.GravadoSubtotal + out.Totals.GravadoTax)
	case AffectationExonerado:
		base := roundMoney(totals.ExoneradoSubtotal + amount)
		out.DiscountBase = base
		out.DiscountFactor = roundFactor(amount / base)
		out.Totals.ExoneradoSubtotal = roundMoney(totals.ExoneradoSubtotal - amount)
		out.Totals.ExoneradoTotal = out.Totals.ExoneradoSubtotal
	case AffectationInafecto:
		base := roundMoney(totals.InafectoSubtotal + amount)
		out.DiscountBase = base
		out.DiscountFactor = roundFactor(amount / base)
		out.Totals.InafectoSubtotal = roundMoney(totals.InafectoSubtotal - amount)
		out.Totals.InafectoTotal = out.Totals.InafectoSubtotal
	default:
		return ApplyDeductionResult{}, fmt.Errorf("grupo de afectación no válido: %s", group)
	}
	out.Totals.Subtotal = roundMoney(
		out.Totals.GravadoSubtotal + out.Totals.ExoneradoSubtotal + out.Totals.InafectoSubtotal,
	)
	out.Totals.TaxAmount = out.Totals.GravadoTax
	out.Totals.Total = roundMoney(
		out.Totals.GravadoTotal + out.Totals.ExoneradoTotal + out.Totals.InafectoTotal,
	)
	return out, nil
}
