package tax

import "testing"

func TestCalcSaleCheckoutGlobalPercentOnly(t *testing.T) {
	r := CalcSaleCheckout(SaleCheckoutInput{
		Lines: []SaleLineInput{{
			UnitPrice: 100, Quantity: 1, IgvAffectationType: "10", PriceIncludesIgv: false,
		}},
		GlobalDiscountMode: "percent", GlobalDiscountValue: 10,
		TaxCfg: cfg18(),
	})
	if r.Subtotal != 90 || r.TaxAmount != 16.2 || r.Total != 106.2 {
		t.Fatalf("got %v/%v/%v want 90/16.2/106.2", r.Subtotal, r.TaxAmount, r.Total)
	}
	if r.GlobalDiscountAmount != 10 {
		t.Fatalf("global=%v", r.GlobalDiscountAmount)
	}
	if len(r.GlobalCharges) != 1 || r.GlobalCharges[0].CodTipo != "02" {
		t.Fatalf("global charges=%+v", r.GlobalCharges)
	}
}

func TestCalcSaleCheckoutGlobalFixedOnly(t *testing.T) {
	r := CalcSaleCheckout(SaleCheckoutInput{
		Lines: []SaleLineInput{{
			UnitPrice: 200, Quantity: 1, IgvAffectationType: "10", PriceIncludesIgv: false,
		}},
		GlobalDiscountMode: "amount", GlobalDiscountValue: 50,
		TaxCfg: cfg18(),
	})
	if r.Subtotal != 150 || r.TaxAmount != 27 || r.Total != 177 {
		t.Fatalf("got %v/%v/%v", r.Subtotal, r.TaxAmount, r.Total)
	}
}

func TestCalcSaleCheckoutLinePercentOnly(t *testing.T) {
	r := CalcSaleCheckout(SaleCheckoutInput{
		Lines: []SaleLineInput{{
			UnitPrice: 200, Quantity: 1, IgvAffectationType: "10", PriceIncludesIgv: false,
			LineDiscountMode: "percent", LineDiscountValue: 10,
		}},
		TaxCfg: cfg18(),
	})
	if r.Subtotal != 180 || r.TaxAmount != 32.4 || r.Total != 212.4 {
		t.Fatalf("got %v/%v/%v want 180/32.4/212.4", r.Subtotal, r.TaxAmount, r.Total)
	}
	if len(r.Lines[0].LineCharges) != 1 || r.Lines[0].LineCharges[0].CodTipo != "00" {
		t.Fatalf("line charges=%+v", r.Lines[0].LineCharges)
	}
	if r.Lines[0].SubtotalAfterLine != 180 {
		t.Fatalf("after line=%v", r.Lines[0].SubtotalAfterLine)
	}
}

func TestCalcSaleCheckoutLineAndGlobalCombined(t *testing.T) {
	r := CalcSaleCheckout(SaleCheckoutInput{
		Lines: []SaleLineInput{{
			UnitPrice: 100, Quantity: 1, IgvAffectationType: "10", PriceIncludesIgv: false,
			LineDiscountMode: "amount", LineDiscountValue: 10,
		}},
		GlobalDiscountMode: "amount", GlobalDiscountValue: 9,
		TaxCfg: cfg18(),
	})
	// 100 - 10 line = 90; global 9 → 81 base; IGV 14.58; total 95.58
	if r.Subtotal != 81 || r.TaxAmount != 14.58 || r.Total != 95.58 {
		t.Fatalf("got %v/%v/%v want 81/14.58/95.58", r.Subtotal, r.TaxAmount, r.Total)
	}
	if r.Lines[0].LineDiscountSubtotal != 10 || r.Lines[0].GlobalDiscountSubtotal != 9 {
		t.Fatalf("breakdown line=%v global=%v", r.Lines[0].LineDiscountSubtotal, r.Lines[0].GlobalDiscountSubtotal)
	}
}

func TestCalcSaleCheckoutNoDiscounts(t *testing.T) {
	r := CalcSaleCheckout(SaleCheckoutInput{
		Lines: []SaleLineInput{{
			UnitPrice: 118, Quantity: 1, IgvAffectationType: "10", PriceIncludesIgv: true,
		}},
		TaxCfg: cfg18(),
	})
	s, tAmt, tot := CalcItem(118, 1, 0, "10", true, cfg18())
	if r.Subtotal != s || r.TaxAmount != tAmt || r.Total != tot {
		t.Fatalf("mismatch no discount")
	}
}

func TestCalcSaleCheckoutExoneradoLineDiscount(t *testing.T) {
	r := CalcSaleCheckout(SaleCheckoutInput{
		Lines: []SaleLineInput{{
			UnitPrice: 50, Quantity: 2, IgvAffectationType: "20", PriceIncludesIgv: false,
			LineDiscountMode: "amount", LineDiscountValue: 10,
		}},
		TaxCfg: cfg18(),
	})
	if r.Subtotal != 90 || r.TaxAmount != 0 || r.Total != 90 {
		t.Fatalf("got %v/%v/%v", r.Subtotal, r.TaxAmount, r.Total)
	}
}

func TestCalcSaleCheckoutGlobal100Percent(t *testing.T) {
	r := CalcSaleCheckout(SaleCheckoutInput{
		Lines: []SaleLineInput{{
			UnitPrice: 100, Quantity: 1, IgvAffectationType: "10", PriceIncludesIgv: false,
		}},
		GlobalDiscountMode: "percent", GlobalDiscountValue: 100,
		TaxCfg: cfg18(),
	})
	if r.Subtotal != 0 || r.Total != 0 {
		t.Fatalf("got sub=%v total=%v", r.Subtotal, r.Total)
	}
}

func TestCalcSaleCheckoutMixedLinesGlobal(t *testing.T) {
	r := CalcSaleCheckout(SaleCheckoutInput{
		Lines: []SaleLineInput{
			{UnitPrice: 100, Quantity: 1, IgvAffectationType: "10", PriceIncludesIgv: false},
			{UnitPrice: 50, Quantity: 1, IgvAffectationType: "20", PriceIncludesIgv: false},
		},
		GlobalDiscountMode: "amount", GlobalDiscountValue: 15,
		TaxCfg: cfg18(),
	})
	// bases after line: 100 + 50 = 150; global 15 → 135 total base; gravado 90 (100-10), exonerado 45 (50-5)
	if r.Subtotal != 135 {
		t.Fatalf("subtotal=%v want 135", r.Subtotal)
	}
	if r.Lines[0].GlobalDiscountSubtotal != 10 || r.Lines[1].GlobalDiscountSubtotal != 5 {
		t.Fatalf("shares %v %v", r.Lines[0].GlobalDiscountSubtotal, r.Lines[1].GlobalDiscountSubtotal)
	}
}

func TestCalcSaleCheckoutBonificacionGravada15(t *testing.T) {
	r := CalcSaleCheckout(SaleCheckoutInput{
		Lines: []SaleLineInput{
			{UnitPrice: 100, Quantity: 1, IgvAffectationType: "10", PriceIncludesIgv: false},
			{UnitPrice: 50, Quantity: 1, IgvAffectationType: "15", PriceIncludesIgv: false},
		},
		TaxCfg: cfg18(),
	})
	if r.Subtotal != 100 || r.TaxAmount != 18 || r.Total != 118 {
		t.Fatalf("chargeable got %v/%v/%v want 100/18/118", r.Subtotal, r.TaxAmount, r.Total)
	}
	if r.Lines[1].Subtotal != 50 || r.Lines[1].TaxAmount != 9 || r.Lines[1].Total != 0 {
		t.Fatalf("bonif line fiscal=%v/%v chargeable=%v want 50/9/0", r.Lines[1].Subtotal, r.Lines[1].TaxAmount, r.Lines[1].Total)
	}
}
