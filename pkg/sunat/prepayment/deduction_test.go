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
