package money

import "testing"

func TestCalcCheckoutDiscountAmountPercent(t *testing.T) {
	got := CalcCheckoutDiscountAmount(9.31, "percent", 15)
	want := RoundSunat(9.31 * 0.15)
	if got != want {
		t.Fatalf("percent: got %.6f want %.6f", got, want)
	}
}

func TestCalcCheckoutDiscountAmountFixed(t *testing.T) {
	got := CalcCheckoutDiscountAmount(9.31, "amount", 2)
	if got != 2 {
		t.Fatalf("amount: got %.6f want 2", got)
	}
	got = CalcCheckoutDiscountAmount(9.31, "amount", 20)
	if got != RoundSunat(9.31) {
		t.Fatalf("amount capped: got %.6f want %.6f", got, RoundSunat(9.31))
	}
}
