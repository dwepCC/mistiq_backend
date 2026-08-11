package money

import "testing"

func TestAllocateSalePaymentReportAmounts_singleCashWithChange(t *testing.T) {
	got := AllocateSalePaymentReportAmounts(12, []SalePaymentLine{{ID: 1, Amount: 20}})
	if got[1] != 12 {
		t.Fatalf("expected 12, got %v", got[1])
	}
}

func TestAllocateSalePaymentReportAmounts_exactPayment(t *testing.T) {
	got := AllocateSalePaymentReportAmounts(12, []SalePaymentLine{
		{ID: 1, Amount: 7},
		{ID: 2, Amount: 5},
	})
	if got[1] != 7 || got[2] != 5 {
		t.Fatalf("expected 7 and 5, got %v", got)
	}
}

func TestAllocateSalePaymentReportAmounts_mixedOverpay(t *testing.T) {
	got := AllocateSalePaymentReportAmounts(20, []SalePaymentLine{
		{ID: 1, Amount: 15},
		{ID: 2, Amount: 10},
	})
	if got[1] != 12 || got[2] != 8 {
		t.Fatalf("expected 12 and 8, got %v", got)
	}
}

func TestAllocateSalePaymentNetAmounts_indexes(t *testing.T) {
	got := AllocateSalePaymentNetAmounts(12, []float64{20})
	if len(got) != 1 || got[0] != 12 {
		t.Fatalf("expected [12], got %v", got)
	}
}
