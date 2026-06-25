package service

import (
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/paymentmethod"
	salessvc "tukifac/internal/sales/service"
	sunatdet "tukifac/pkg/sunat/detraccion"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestPrepareDetractionSalePaymentsAllowCredit_partial(t *testing.T) {
	eval := detractionEval1180(t)
	out, isCredit, err := salessvc.PrepareDetractionSalePaymentsAllowCredit(
		[]salessvc.PaymentInput{{Method: "cash", Amount: 500}},
		1180,
		eval,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !isCredit {
		t.Fatal("expected credit sale")
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(out))
	}
	if out[1].Method != paymentmethod.CodeDetraccionBN {
		t.Fatalf("expected spot line: %+v", out[1])
	}
}

func TestSaleBalance_detraccion1180(t *testing.T) {
	sale := database.TenantSale{Total: 1180, Status: "credit"}
	det := &database.TenantSaleDetraccion{
		NetPayablePen:       1132.80,
		DetractionAmountPen: 47.20,
		BnConfirmationStatus: BnStatusPending,
	}
	payments := []database.TenantSalePayment{
		{Method: "cash", Amount: 500},
		{Method: paymentmethod.CodeDetraccionBN, Amount: 47.20},
	}
	target, paid, due, spot, spotPending, bn := SaleBalance(sale, det, payments)
	if target != 1132.80 || paid != 500 || due != 632.80 {
		t.Fatalf("balance: target=%v paid=%v due=%v", target, paid, due)
	}
	if spot != 47.20 || spotPending != 47.20 || bn != BnStatusPending {
		t.Fatalf("spot: amt=%v pending=%v bn=%q", spot, spotPending, bn)
	}
	if !HasOpenReceivable(sale, det, payments) {
		t.Fatal("expected open receivable")
	}
}

func TestReceivableService_ConfirmBN(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:recv?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantSale{},
		&database.TenantSaleDetraccion{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	sale := database.TenantSale{Number: "F001-00000001", Total: 1180, Status: "credit"}
	if err := db.Create(&sale).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSaleDetraccion{
		SaleID: sale.ID, NetPayablePen: 1132.80, DetractionAmountPen: 47.20,
		BnConfirmationStatus: BnStatusPending,
	}).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewReceivableService(db)
	row, err := svc.ConfirmBN(sale.ID, ConfirmBNInput{Status: BnStatusConfirmed, Reference: "OP-123"})
	if err != nil {
		t.Fatal(err)
	}
	if row.BnConfirmationStatus != BnStatusConfirmed || row.BnConfirmationReference != "OP-123" {
		t.Fatalf("unexpected row: %+v", row)
	}
}

func detractionEval1180(t *testing.T) sunatdet.CalcResult {
	t.Helper()
	cat, err := sunatdet.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	res, err := sunatdet.Evaluate(cat, sunatdet.CalcInput{
		OperationTypeCode: "1001",
		SunatDocCode:      "01",
		Currency:          "PEN",
		GravadoTotalPEN:   1180,
		SaleTotalPEN:      1180,
		GoodCode:          "014",
		BankAccount:       "0004-1234567890",
		PaymentMethodCode: "001",
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}
