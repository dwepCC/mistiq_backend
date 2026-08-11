package tax

import "testing"

func TestBuildAllowanceFactor(t *testing.T) {
	ch := buildAllowance(AllowanceCodeLineDiscountAffectsIGV, 200, 20)
	if ch.Factor != 0.1 {
		t.Fatalf("factor=%v want 0.1", ch.Factor)
	}
	if ch.Monto != 20 || ch.MontoBase != 200 {
		t.Fatalf("monto=%v base=%v", ch.Monto, ch.MontoBase)
	}
}
