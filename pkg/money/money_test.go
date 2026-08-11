package money

import "testing"

func TestPaidCoversTotal_allowsOverpayment(t *testing.T) {
	if !PaidCoversTotal(100, 90) {
		t.Fatal("100 debe cubrir total 90 (vuelto permitido)")
	}
}

func TestPaidCoversTotal_rejectsUnderpayment(t *testing.T) {
	if PaidCoversTotal(80, 90) {
		t.Fatal("80 no debe cubrir total 90")
	}
}

func TestCalcPaymentChange(t *testing.T) {
	if got := CalcPaymentChange(100, 90); got != 10 {
		t.Fatalf("vuelto: got %v want 10", got)
	}
	if got := CalcPaymentChange(90, 90); got != 0 {
		t.Fatalf("pago exacto: got %v want 0", got)
	}
}
