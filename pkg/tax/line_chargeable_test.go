package tax

import "testing"

func TestIsGravadoOperacionNoOnerosa_Catalog11To16(t *testing.T) {
	for _, code := range []string{"11", "12", "13", "14", "15", "16"} {
		if !IsGravadoOperacionNoOnerosa(code) {
			t.Fatalf("code %s should be gravado no oneroso", code)
		}
	}
	for _, code := range []string{"10", "17", "20", "21", "30", "40"} {
		if IsGravadoOperacionNoOnerosa(code) {
			t.Fatalf("code %s should not be gravado no oneroso", code)
		}
	}
}

func TestLineChargeableTotalImpuestos_GravadoNoOnerosoZeroesReferentialIGV(t *testing.T) {
	got := LineChargeableTotalImpuestos("15", 5.34, 0)
	if got != 0 {
		t.Fatalf("chargeable=%v want 0 (legacy total_taxes)", got)
	}
}

func TestLineChargeableTotalImpuestos_GravadoNoOnerosoKeepsPlasticBag(t *testing.T) {
	got := LineChargeableTotalImpuestos("15", 1.68, 0.5)
	if got != 0.5 {
		t.Fatalf("chargeable=%v want 0.5 (ICBPER only)", got)
	}
}

func TestLineChargeableTotalImpuestos_OnerosoUnchanged(t *testing.T) {
	got := LineChargeableTotalImpuestos("10", 3.81, 0)
	if got != 3.81 {
		t.Fatalf("chargeable=%v want 3.81", got)
	}
}
