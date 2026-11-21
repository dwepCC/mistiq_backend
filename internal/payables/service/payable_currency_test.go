package service

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Compras en USD: la CxP queda en la moneda del documento; el resumen consolida en soles con el TC de cada compra
// y expone aparte la parte en dólares.
func TestPayables_USDRowsAndSummary(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	svc := NewPayableService(db)
	rate := 3.5
	mk := func(num, cur string, rt *float64, amount, paid float64) {
		p := &database.TenantPurchase{BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: num, IssueDate: time.Now(),
			Total: amount, Currency: cur, ExchangeRate: rt, Status: "received"}
		if err := db.Create(p).Error; err != nil {
			t.Fatal(err)
		}
		st := "pending"
		if paid > 0 {
			st = "partial"
		}
		db.Create(&database.TenantPurchasePayable{PurchaseID: p.ID, OriginalAmount: amount, PaidAmount: paid, Status: st})
	}
	mk("1", "PEN", nil, 100, 0)
	mk("2", "USD", &rate, 200, 50) // saldo USD 150 = S/ 525

	rows, _, err := svc.List(ListFilter{BranchID: 1, Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	cur := map[string]PayableRow{}
	for _, r := range rows {
		cur[r.PurchaseNumber] = r
	}
	if r := cur["F001-2"]; r.Currency != "USD" || r.ExchangeRate == nil || *r.ExchangeRate != 3.5 || r.Due != 150 {
		t.Errorf("fila USD: %+v", r)
	}
	if r := cur["F001-1"]; r.Currency != "PEN" || r.ExchangeRate != nil {
		t.Errorf("fila PEN: %+v", r)
	}
	sum, err := svc.Summary(1)
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalDue != 625 || sum.TotalDueUSD != 150 { // 100 + 150×3.5
		t.Errorf("resumen: total_due=%v total_due_usd=%v, quiere 625 y 150", sum.TotalDue, sum.TotalDueUSD)
	}
}

func TestPayables_OverpayMessageUsesCurrencySymbol(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	svc := NewPayableService(db)
	rate := 3.5
	p := &database.TenantPurchase{BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "9", IssueDate: time.Now(),
		Total: 100, Currency: "USD", ExchangeRate: &rate, Status: "received"}
	db.Create(p)
	db.Create(&database.TenantPurchasePayable{PurchaseID: p.ID, OriginalAmount: 100, Status: "pending"})
	openPayableSession(t, db, 1, 1, 1)
	err := svc.Pay(p.ID, PayInput{Amount: 500, Method: "cash", UserID: 1})
	if err == nil || !strings.Contains(err.Error(), "US$") {
		t.Errorf("el mensaje de sobrepago debe usar US$, err=%v", err)
	}
}
