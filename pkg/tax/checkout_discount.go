package tax

import "tukifac/pkg/money"

// SubtotalDiscountToLineDiscount convierte descuento en base imponible al monto `discount`
// que espera CalcItem (resta del importe bruto qty*price).
func SubtotalDiscountToLineDiscount(
	unitPrice, quantity, subtotalDiscount float64,
	igvAffectationType string,
	priceIncludesIgv bool,
	taxCfg Config,
) float64 {
	disc := max0(subtotalDiscount)
	if disc <= 0 {
		return 0
	}
	rate := taxCfg.EffectiveRate(igvAffectationType)
	if rate == 0 {
		return money.RoundSunat(disc)
	}
	if priceIncludesIgv {
		return money.RoundSunat(disc * (1 + rate/100))
	}
	return money.RoundSunat(disc)
}

// CalcItemWithSubtotalDiscount aplica descuento sobre la base imponible y recalcula IGV/total.
func CalcItemWithSubtotalDiscount(
	unitPrice, quantity, subtotalDiscount float64,
	igvAffectationType string,
	priceIncludesIgv bool,
	taxCfg Config,
) (subtotal, taxAmount, total float64) {
	baseSub, _, _ := CalcItem(unitPrice, quantity, 0, igvAffectationType, priceIncludesIgv, taxCfg)
	disc := max0(subtotalDiscount)
	newSub := money.RoundSunat(max0(baseSub - disc))
	rate := taxCfg.EffectiveRate(igvAffectationType)
	if rate == 0 {
		return newSub, 0, newSub
	}
	taxAmount = money.RoundSunat(newSub * (rate / 100))
	total = money.RoundSunat(newSub + taxAmount)
	return newSub, taxAmount, total
}

// LineSubtotalDiscountFromStored infiere el descuento en base imponible a partir del valor
// persistido en TenantSaleItem.Discount (formato CalcItem).
func LineSubtotalDiscountFromStored(
	quantity, unitPrice, storedDiscount, subtotal, taxAmount float64,
	igvAffectationType string,
	priceIncludesIgv bool,
	taxCfg Config,
) float64 {
	disc := max0(storedDiscount)
	if disc <= 0 {
		return 0
	}
	if taxAmount <= 0.000001 {
		return money.RoundSunat(disc)
	}
	rate := taxCfg.EffectiveRate(igvAffectationType)
	if rate <= 0 {
		return money.RoundSunat(disc)
	}
	effRate := 0.0
	if subtotal > 0 {
		effRate = taxAmount / subtotal
	}
	if effRate <= 0 {
		return money.RoundSunat(disc)
	}
	gross := quantity*unitPrice - disc
	subIfIgvIncluded := gross / (1 + effRate)
	if money.RoundSunat(subIfIgvIncluded) == money.RoundSunat(subtotal) {
		return money.RoundSunat(disc / (1 + effRate))
	}
	if !priceIncludesIgv {
		return money.RoundSunat(disc)
	}
	return money.RoundSunat(disc / (1 + rate/100))
}

// ApplyGlobalCheckoutDiscount reparte descuento global sobre subtotales de línea y devuelve
// descuentos en base imponible por índice.
func ApplyGlobalCheckoutDiscount(lineSubtotals []float64, discountAmount float64) []float64 {
	return money.DistributeCheckoutDiscountToLines(lineSubtotals, discountAmount)
}

func max0(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}
