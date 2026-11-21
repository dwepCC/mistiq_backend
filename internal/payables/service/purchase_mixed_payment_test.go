package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"

	purchasesvc "tukifac/internal/purchases/service"
)

// Pago mixto en una compra: efectivo + Yape. Queda como cuenta por pagar saldada con un pago por línea; caja y banco se mueven por
// línea y la anulación revierte todo.
func TestPurchaseMixedPayment_CashPlusYape_VoidReversesAll(t *testing.T) {
	db := setupPayableFullFlowTestDB(t)
	openFlowSession(t, db, 40, 1, 1)
	psvc := purchasesvc.NewPurchaseService(db)
	pid := newFlowProduct(t, db, "PM1")

	in := creditPurchaseInput(1, 1, "9001", pid, 1000)
	// Total real: 1000/1.18 sin IGV → 1000.00 con IGV (el helper redondea); se leen del resultado.
	in.Payments = []purchasesvc.PurchasePaymentLine{{Method: "cash", Amount: 400}, {Method: "yape", Amount: 600, Reference: "OP-1"}}
	p, err := psvc.Create(in)
	if err != nil {
		t.Fatalf("Create mixto: %v", err)
	}
	if p.PaymentMethod != "" {
		t.Errorf("la compra mixta no guarda un método único: %q", p.PaymentMethod)
	}
	var pays []database.TenantPurchasePayment
	db.Where("purchase_id = ?", p.ID).Order("id").Find(&pays)
	if len(pays) != 2 || pays[0].Method != "cash" || pays[1].Reference != "OP-1" || pays[0].Notes != purchasesvc.PurchaseUpfrontPaymentNote {
		t.Fatalf("pagos al registrar: %+v", pays)
	}
	var pb database.TenantPurchasePayable
	db.Where("purchase_id = ?", p.ID).First(&pb)
	if pb.Status != "paid" || pb.PaidAmount != p.Total {
		t.Errorf("la cuenta por pagar debe quedar saldada: %+v", pb)
	}
	var cashOut float64
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ? AND type = ?", p.ID, "expense").Select("COALESCE(SUM(amount),0)").Scan(&cashOut)
	var yape database.TenantBankAccount
	db.Where("name = ?", "Yape").First(&yape)
	if cashOut != 400 || yape.Balance != -600 {
		t.Fatalf("efectivo -400 y Yape -600: caja=%v yape=%v", cashOut, yape.Balance)
	}
	if up := psvc.UpfrontPaymentsByPurchase([]uint{p.ID})[p.ID]; len(up) != 2 {
		t.Errorf("resumen de pagos al registrar: %+v", up)
	}

	if err := psvc.Void(p.ID, 1); err != nil {
		t.Fatalf("Void: %v", err)
	}
	db.Where("name = ?", "Yape").First(&yape)
	var net float64
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", p.ID).
		Select("COALESCE(SUM(CASE WHEN type = 'income' THEN amount ELSE -amount END),0)").Scan(&net)
	if yape.Balance != 0 || net != 0 {
		t.Errorf("la anulación revierte caja y banco: yape=%v neto caja=%v", yape.Balance, net)
	}
}

func TestPurchaseMixedPayment_Validations(t *testing.T) {
	db := setupPayableFullFlowTestDB(t)
	openFlowSession(t, db, 41, 1, 1)
	psvc := purchasesvc.NewPurchaseService(db)
	pid := newFlowProduct(t, db, "PM2")

	bad := creditPurchaseInput(1, 1, "9002", pid, 1000)
	bad.Payments = []purchasesvc.PurchasePaymentLine{{Method: "cash", Amount: 300}, {Method: "yape", Amount: 300}}
	if _, err := psvc.Create(bad); err == nil || !strings.Contains(err.Error(), "debe ser igual al total") {
		t.Errorf("suma distinta del total debe rechazarse, err=%v", err)
	}
	both := creditPurchaseInput(1, 1, "9003", pid, 1000)
	both.PaymentMethod = "cash"
	both.Payments = []purchasesvc.PurchasePaymentLine{{Method: "cash", Amount: 500}, {Method: "yape", Amount: 500}}
	if _, err := psvc.Create(both); err == nil {
		t.Error("método único y pago mixto a la vez debe rechazarse")
	}
	var n int64
	db.Model(&database.TenantPurchase{}).Count(&n)
	if n != 0 {
		t.Errorf("un rechazo no debe dejar compras: %d", n)
	}
}
