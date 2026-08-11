package money

import "testing"

func TestDistributeCheckoutDiscountToLines(t *testing.T) {
	got := DistributeCheckoutDiscountToLines([]float64{60, 40}, 10)
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0] != 6 || got[1] != 4 {
		t.Fatalf("got=%v want [6 4]", got)
	}
	sum := RoundSunat(got[0] + got[1])
	if sum != 10 {
		t.Fatalf("sum=%v", sum)
	}
}

func TestDistributeCheckoutDiscountCapsAtBase(t *testing.T) {
	got := DistributeCheckoutDiscountToLines([]float64{5}, 20)
	if got[0] != 5 {
		t.Fatalf("got=%v want 5", got[0])
	}
}
