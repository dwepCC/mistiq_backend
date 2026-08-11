package service

import (
	"testing"

	"tukifac/pkg/database"
)

func TestCancelledSaleReasonFromNotes(t *testing.T) {
	got := cancelledSaleReasonFromNotes("Pedido mesa 5 | ANULADA: error de cobro")
	if got != "error de cobro" {
		t.Fatalf("expected parsed reason, got %q", got)
	}
	if cancelledSaleReasonFromNotes("") != "" {
		t.Fatal("expected empty reason")
	}
}

func TestCancelledSaleRowKey_dedupesByAmount(t *testing.T) {
	a := cancelledSaleRowKey(1, "yape", 10.5)
	b := cancelledSaleRowKey(1, "YAPE", 10.5)
	if a != b {
		t.Fatalf("expected same key, got %q vs %q", a, b)
	}
	if cancelledSaleRowKey(1, "yape", 11) == a {
		t.Fatal("different amounts should produce different keys")
	}
}

func TestVoidMovementRowKeys(t *testing.T) {
	saleID := uint(42)
	seen := voidMovementRowKeys([]database.TenantCashMovement{
		{SaleID: &saleID, PaymentMethod: "cash", Amount: 25},
	})
	key := cancelledSaleRowKey(42, "efectivo", 25)
	if _, ok := seen[key]; !ok {
		t.Fatalf("expected key %q in seen map", key)
	}
}
