package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestNormalizeRelatedDocType(t *testing.T) {
	cases := map[string]string{
		"01":      "01",
		"Factura": "01",
		"FACTURA": "01",
		"Boleta":  "03",
		"03":      "03",
		"NC":      "07",
		"Nota de crédito": "07",
		"Ticket":          "12",
	}
	for in, want := range cases {
		if got := normalizeRelatedDocType(in); got != want {
			t.Errorf("normalizeRelatedDocType(%q) = %q, want %q", in, got, want)
		}
	}
}

// Compra en USD con TC propio: el prefill de la retención usa ese TC (no uno consultado por fecha); en soles no lleva TC.
func TestRetentionPrefillFromPurchase_UsesPurchaseExchangeRateForUSD(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantPurchase{}); err != nil {
		t.Fatal(err)
	}
	contact := uint(1)
	rate := 3.62
	usd := database.TenantPurchase{BranchID: 1, ContactID: &contact, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "10",
		IssueDate: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC), Total: 1000, Currency: "USD", ExchangeRate: &rate, Status: "received"}
	pen := database.TenantPurchase{BranchID: 1, ContactID: &contact, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "11",
		IssueDate: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC), Total: 1000, Currency: "PEN", Status: "received"}
	db.Create(&usd)
	db.Create(&pen)
	svc := &BillingService{db: db}

	in := CreateRetentionInput{}
	if err := svc.applyRetentionPrefillFromPurchase(&in, usd.ID); err != nil {
		t.Fatal(err)
	}
	d := in.Details[0]
	if d.Moneda != "USD" || d.TipoCambio == nil || d.TipoCambio.Factor != 3.62 || d.TipoCambio.MonedaRef != "USD" || d.TipoCambio.MonedaObj != "PEN" {
		t.Errorf("USD: moneda=%q tipo_cambio=%+v", d.Moneda, d.TipoCambio)
	}
	in2 := CreateRetentionInput{}
	if err := svc.applyRetentionPrefillFromPurchase(&in2, pen.ID); err != nil {
		t.Fatal(err)
	}
	if in2.Details[0].TipoCambio != nil {
		t.Errorf("PEN no lleva tipo de cambio: %+v", in2.Details[0].TipoCambio)
	}
}
