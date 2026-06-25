package service

import (
	"testing"

	"tukifac/pkg/paymentmethod"
)

func TestMovementRowChannel_detractionExcludedFromElectronic(t *testing.T) {
	row := MovementReportRow{Type: "venta", PaymentMethod: paymentmethod.CodeDetraccionBN, Amount: 47.2}
	if ch := movementRowChannel(row); ch != "detraction" {
		t.Fatalf("expected detraction, got %q", ch)
	}
	if IsDetractionPaymentMethod(paymentmethod.CodeDetraccionBN) != true {
		t.Fatal("IsDetractionPaymentMethod should be true")
	}
}

func TestMovementRowChannel_cashVsElectronic(t *testing.T) {
	cash := MovementReportRow{Type: "venta", PaymentMethod: "cash", Amount: 100}
	if movementRowChannel(cash) != "cash" {
		t.Fatalf("expected cash, got %q", movementRowChannel(cash))
	}
	yape := MovementReportRow{Type: "venta", PaymentMethod: "yape", Amount: 50}
	if movementRowChannel(yape) != "electronic" {
		t.Fatalf("expected electronic, got %q", movementRowChannel(yape))
	}
}
