package tax

import "testing"

func cfg18() Config {
	return Config{TaxRate: 18, IgvRegime: "standard"}
}

// Caso 1: subtotal 100, descuento 10% → base 90, IGV 16.20, total 106.20
func TestGlobalDiscountPercentOnSubtotal(t *testing.T) {
	taxCfg := cfg18()
	sub, taxAmt, total := CalcItemWithSubtotalDiscount(100, 1, 10, "10", false, taxCfg)
	if sub != 90 || taxAmt != 16.2 || total != 106.2 {
		t.Fatalf("got sub=%v tax=%v total=%v want 90/16.2/106.2", sub, taxAmt, total)
	}
}

// Caso 2: subtotal 200, descuento fijo 50 → base 150, IGV 27, total 177
func TestGlobalDiscountFixedOnSubtotal(t *testing.T) {
	taxCfg := cfg18()
	sub, taxAmt, total := CalcItemWithSubtotalDiscount(200, 1, 50, "10", false, taxCfg)
	if sub != 150 || taxAmt != 27 || total != 177 {
		t.Fatalf("got sub=%v tax=%v total=%v want 150/27/177", sub, taxAmt, total)
	}
}

// Caso 4: sin descuento → mismos resultados que CalcItem
func TestGlobalDiscountZeroMatchesCalcItem(t *testing.T) {
	taxCfg := cfg18()
	s1, t1, tot1 := CalcItem(118, 1, 0, "10", true, taxCfg)
	s2, t2, tot2 := CalcItemWithSubtotalDiscount(118, 1, 0, "10", true, taxCfg)
	if s1 != s2 || t1 != t2 || tot1 != tot2 {
		t.Fatalf("mismatch zero discount: %v/%v/%v vs %v/%v/%v", s1, t1, tot1, s2, t2, tot2)
	}
}

// Caso 5: descuento 100% → base 0
func TestGlobalDiscountFullPercent(t *testing.T) {
	taxCfg := cfg18()
	sub, taxAmt, total := CalcItemWithSubtotalDiscount(100, 1, 100, "10", false, taxCfg)
	if sub != 0 || taxAmt != 0 || total != 0 {
		t.Fatalf("got sub=%v tax=%v total=%v want 0/0/0", sub, taxAmt, total)
	}
}

func TestSubtotalDiscountToLineDiscountPriceIncludesIgv(t *testing.T) {
	taxCfg := cfg18()
	lineDisc := SubtotalDiscountToLineDiscount(118, 1, 10, "10", true, taxCfg)
	sub, _, _ := CalcItem(118, 1, lineDisc, "10", true, taxCfg)
	if sub != 90 {
		t.Fatalf("sub=%v want 90", sub)
	}
}

func TestApplyGlobalCheckoutDiscountDistributes(t *testing.T) {
	shares := ApplyGlobalCheckoutDiscount([]float64{100}, 10)
	if len(shares) != 1 || shares[0] != 10 {
		t.Fatalf("got=%v", shares)
	}
}
