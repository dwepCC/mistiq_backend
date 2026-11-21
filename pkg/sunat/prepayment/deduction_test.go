package prepayment

import "testing"

func TestApplyDeductionToSaleTotals_Gravado(t *testing.T) {
	totals := SaleGroupTotals{
		GravadoSubtotal: 100,
		GravadoTax:      18,
		GravadoTotal:    118,
		Subtotal:        100,
		TaxAmount:       18,
		Total:           118,
	}
	res, err := ApplyDeductionToSaleTotals(AffectationGravado, totals, 50, 59, 18)
	if err != nil {
		t.Fatal(err)
	}
	if res.Totals.GravadoSubtotal != 50 {
		t.Fatalf("gravado subtotal: got %v want 50", res.Totals.GravadoSubtotal)
	}
	if res.Totals.GravadoTax != 9 {
		t.Fatalf("gravado tax: got %v want 9", res.Totals.GravadoTax)
	}
	if res.Totals.Total != 59 {
		t.Fatalf("total: got %v want 59", res.Totals.Total)
	}
	if res.DiscountCode != DiscountCodeGravadoAnticipo {
		t.Fatalf("discount code: %s", res.DiscountCode)
	}
}

func TestDeductionTotalFromBaseCapped_Rounding(t *testing.T) {
	total := DeductionTotalFromBaseCapped(184.75, AffectationGravado, 18, 218)
	if total != 218 {
		t.Fatalf("total capped: got %v want 218", total)
	}
}

func TestSaleGroupDeductibleBase(t *testing.T) {
	totals := SaleGroupTotals{GravadoSubtotal: 15.25, ExoneradoSubtotal: 10, InafectoSubtotal: 5}
	if SaleGroupDeductibleBase(AffectationGravado, totals) != 15.25 {
		t.Fatal("gravado base")
	}
}

// Casos reales del tenant 20612257320 (oct-2026): el IGV recalculado sobre la base redondeada
// daba 100.01 y 44.99 en vez de 100.00 y 45.00.
func TestApplyDeductionToSaleTotals_TotalExactoSinDerivaDeCentimo(t *testing.T) {
	cases := []struct {
		name                          string
		gSub, gTax, gTotal, base, ded float64
		wantSub, wantTax, wantTotal   float64
	}{
		{"130 menos anticipo de 30", 110.17, 19.83, 130, 25.42, 30, 84.75, 15.25, 100},
		{"79.50 menos anticipo de 34.50", 67.37, 12.13, 79.5, 29.24, 34.5, 38.13, 6.87, 45},
		{"59 menos anticipo de 50", 50, 9, 59, 42.37, 50, 7.63, 1.37, 9},
	}
	for _, c := range cases {
		totals := SaleGroupTotals{GravadoSubtotal: c.gSub, GravadoTax: c.gTax, GravadoTotal: c.gTotal, Subtotal: c.gSub, TaxAmount: c.gTax, Total: c.gTotal}
		res, err := ApplyDeductionToSaleTotals(AffectationGravado, totals, c.base, c.ded, 18)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if res.Totals.Subtotal != c.wantSub || res.Totals.TaxAmount != c.wantTax || res.Totals.Total != c.wantTotal {
			t.Fatalf("%s: got sub=%v tax=%v total=%v want %v/%v/%v", c.name, res.Totals.Subtotal, res.Totals.TaxAmount, res.Totals.Total, c.wantSub, c.wantTax, c.wantTotal)
		}
	}
}
