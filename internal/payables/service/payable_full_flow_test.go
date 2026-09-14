package service

import (
	"fmt"
	"testing"
	"time"

	purchasesvc "tukifac/internal/purchases/service"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Decisión A (Fase 2): toda compra exige y registra una Caja abierta, incluso 100% a crédito.
// Estos tests ejercitan el camino real de producción de punta a punta — purchase_service.Create
// (no un fixture manual) seguido de PayableService.Pay — para demostrar exactamente los 6
// escenarios pedidos, usando ambos servicios reales (sin import cycle: purchases/service no
// importa payables/service).

func setupPayableFullFlowTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantProduct{}, &database.TenantPurchase{}, &database.TenantPurchaseItem{},
		&database.TenantPurchasePayable{}, &database.TenantPurchasePayment{},
		&database.TenantProductStock{}, &database.TenantStockMovement{}, &database.TenantInventoryOperationType{},
		&database.TenantProductSerial{}, &database.TenantBankAccount{}, &database.TenantBankMovement{},
		&database.TenantPaymentMethod{}, &database.TenantCashSession{}, &database.TenantCashMovement{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	yapeAcc := &database.TenantBankAccount{Name: "Yape", Type: "wallet", Active: true}
	if err := db.Create(yapeAcc).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "yape", Name: "Yape", BankAccountID: &yapeAcc.ID, Active: true, DestinationType: "bank_account"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true, DestinationType: "cash"}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func openFlowSession(t *testing.T, db *gorm.DB, sessionID, branchID, userID uint) {
	t.Helper()
	if err := db.Create(&database.TenantCashSession{
		ID: sessionID, BranchID: branchID, UserID: userID, OpenedBy: userID, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func newFlowProduct(t *testing.T, db *gorm.DB, code string) uint {
	t.Helper()
	p := &database.TenantProduct{Code: code, Name: code, Type: "product", Unit: "NIU",
		SalePrice: 10, PurchasePrice: 5, TaxRate: 18, IgvAffectationType: "10", ManageStock: false, Active: true}
	if err := db.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func creditPurchaseInput(branchID, userID uint, number string, pid uint, total float64) purchasesvc.CreatePurchaseInput {
	return purchasesvc.CreatePurchaseInput{
		BranchID: branchID, UserID: userID, DocType: "FACTURA", Series: "F001", Number: number,
		IssueDate: time.Now(), PaymentMethod: "", // crédito
		Items: []purchasesvc.PurchaseItemInput{{
			ProductID: &pid, Description: "item", Quantity: 1, UnitCost: total / 1.18,
			IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	}
}

// TEST 1 — Compra crédito en Caja 25 → Purchase.CashSessionID = 25.
func TestFullFlow_compraCredito_Caja25_purchaseQuedaEnCaja25(t *testing.T) {
	db := setupPayableFullFlowTestDB(t)
	openFlowSession(t, db, 25, 1, 1)
	psvc := purchasesvc.NewPurchaseService(db)
	pid := newFlowProduct(t, db, "P1")

	purchase, err := psvc.Create(creditPurchaseInput(1, 1, "1", pid, 5000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if purchase.CashSessionID == nil || *purchase.CashSessionID != 25 {
		t.Fatalf("purchase.CashSessionID = %v, want 25", purchase.CashSessionID)
	}
	var count int64
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", purchase.ID).Count(&count)
	var bankCount int64
	db.Model(&database.TenantBankMovement{}).Where("purchase_id = ?", purchase.ID).Count(&bankCount)
	if count != 0 || bankCount != 0 {
		t.Errorf("compra a crédito sin pago no debe generar ningún movimiento: cash=%d bank=%d", count, bankCount)
	}
}

// TEST 2 y 3 — Pagos posteriores en Cajas 30 y 35: la compra JAMÁS cambia de Caja 25; cada pago
// queda en la suya.
func TestFullFlow_pagosPosteriores_Caja30YCaja35_purchaseNuncaCambia(t *testing.T) {
	db := setupPayableFullFlowTestDB(t)
	openFlowSession(t, db, 25, 1, 1)
	openFlowSession(t, db, 30, 1, 2)
	openFlowSession(t, db, 35, 1, 3)
	psvc := purchasesvc.NewPurchaseService(db)
	paysvc := NewPayableService(db)
	pid := newFlowProduct(t, db, "P2")

	purchase, err := psvc.Create(creditPurchaseInput(1, 1, "2", pid, 5000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if purchase.CashSessionID == nil || *purchase.CashSessionID != 25 {
		t.Fatalf("purchase.CashSessionID = %v, want 25", purchase.CashSessionID)
	}

	// Pago 1: Caja 30, efectivo.
	sess30 := uint(30)
	if err := paysvc.Pay(purchase.ID, PayInput{Amount: 2000, Method: "cash", CashSessionID: &sess30, UserID: 2}); err != nil {
		t.Fatalf("pago 1: %v", err)
	}
	var reloaded database.TenantPurchase
	db.First(&reloaded, purchase.ID)
	if reloaded.CashSessionID == nil || *reloaded.CashSessionID != 25 {
		t.Fatalf("tras pago 1: purchase.CashSessionID = %v, want 25 (no debe cambiar)", reloaded.CashSessionID)
	}
	var payment1 database.TenantPurchasePayment
	db.Where("purchase_id = ? AND method = ?", purchase.ID, "cash").First(&payment1)
	if payment1.CashSessionID == nil || *payment1.CashSessionID != 30 {
		t.Fatalf("payment1.CashSessionID = %v, want 30", payment1.CashSessionID)
	}

	// Pago 2: Caja 35, Yape.
	sess35 := uint(35)
	if err := paysvc.Pay(purchase.ID, PayInput{Amount: 1000, Method: "yape", CashSessionID: &sess35, UserID: 3}); err != nil {
		t.Fatalf("pago 2: %v", err)
	}
	db.First(&reloaded, purchase.ID)
	if reloaded.CashSessionID == nil || *reloaded.CashSessionID != 25 {
		t.Fatalf("tras pago 2: purchase.CashSessionID = %v, want 25 (no debe cambiar)", reloaded.CashSessionID)
	}
	var payment2 database.TenantPurchasePayment
	db.Where("purchase_id = ? AND method = ?", purchase.ID, "yape").First(&payment2)
	if payment2.CashSessionID == nil || *payment2.CashSessionID != 35 {
		t.Fatalf("payment2.CashSessionID = %v, want 35", payment2.CashSessionID)
	}

	payable := getPayable(t, db, purchase.ID)
	_, due := PayableBalance(payable)
	if due != 2000 {
		t.Errorf("saldo pendiente = %v, want 2000", due)
	}
}

// TEST 4 — Compra a crédito SIN Caja abierta: debe rechazarse.
func TestFullFlow_compraCreditoSinCajaAbierta_rechaza(t *testing.T) {
	db := setupPayableFullFlowTestDB(t)
	psvc := purchasesvc.NewPurchaseService(db)
	pid := newFlowProduct(t, db, "P4")

	_, err := psvc.Create(creditPurchaseInput(1, 9, "4", pid, 5000))
	if err == nil {
		t.Fatal("se esperaba error: compra a crédito sin sesión de caja abierta")
	}
	var count int64
	db.Model(&database.TenantPurchase{}).Count(&count)
	if count != 0 {
		t.Errorf("no debió persistirse ninguna compra, got %d", count)
	}
}

// TEST 5 — Compra al contado: comportamiento correcto (ya exigía sesión desde P0; se confirma
// aquí junto al resto del flujo de Fase 2 para que quede documentado en el mismo lugar).
func TestFullFlow_compraContado_comportamientoCorrecto(t *testing.T) {
	db := setupPayableFullFlowTestDB(t)
	openFlowSession(t, db, 25, 1, 1)
	psvc := purchasesvc.NewPurchaseService(db)
	pid := newFlowProduct(t, db, "P5")

	input := purchasesvc.CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "5",
		IssueDate: time.Now(), PaymentMethod: "cash",
		Items: []purchasesvc.PurchaseItemInput{{
			ProductID: &pid, Description: "item", Quantity: 1, UnitCost: 100, IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	}
	purchase, err := psvc.Create(input)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if purchase.CashSessionID == nil || *purchase.CashSessionID != 25 {
		t.Fatalf("purchase.CashSessionID = %v, want 25", purchase.CashSessionID)
	}
	var cashMov database.TenantCashMovement
	if err := db.Where("purchase_id = ?", purchase.ID).First(&cashMov).Error; err != nil {
		t.Fatal(err)
	}
	if cashMov.CashSessionID != 25 {
		t.Fatalf("cash_movement.CashSessionID = %v, want 25", cashMov.CashSessionID)
	}
	// Compra al contado no genera TenantPurchasePayable (no hay CxP).
	var payableCount int64
	db.Model(&database.TenantPurchasePayable{}).Where("purchase_id = ?", purchase.ID).Count(&payableCount)
	if payableCount != 0 {
		t.Errorf("una compra al contado no debe generar CxP, got %d", payableCount)
	}
}

// TEST 6 — Pago digital posterior: movimiento bancario en la Caja del pago, cero movimiento de
// efectivo.
func TestFullFlow_pagoDigitalPosterior_bankMovementEnSuCaja_sinEfectivo(t *testing.T) {
	db := setupPayableFullFlowTestDB(t)
	openFlowSession(t, db, 25, 1, 1)
	openFlowSession(t, db, 30, 1, 2)
	psvc := purchasesvc.NewPurchaseService(db)
	paysvc := NewPayableService(db)
	pid := newFlowProduct(t, db, "P6")

	purchase, err := psvc.Create(creditPurchaseInput(1, 1, "6", pid, 5000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	sess30 := uint(30)
	if err := paysvc.Pay(purchase.ID, PayInput{Amount: 1500, Method: "yape", CashSessionID: &sess30, UserID: 2}); err != nil {
		t.Fatalf("Pay: %v", err)
	}

	var reloaded database.TenantPurchase
	db.First(&reloaded, purchase.ID)
	if reloaded.CashSessionID == nil || *reloaded.CashSessionID != 25 {
		t.Fatalf("purchase.CashSessionID = %v, want 25 (no debe cambiar)", reloaded.CashSessionID)
	}
	var bankMov database.TenantBankMovement
	if err := db.Where("purchase_id = ?", purchase.ID).First(&bankMov).Error; err != nil {
		t.Fatal(err)
	}
	if bankMov.CashSessionID == nil || *bankMov.CashSessionID != 30 || bankMov.Amount != 1500 {
		t.Fatalf("bank movement inesperado: %+v", bankMov)
	}
	var cashCount int64
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", purchase.ID).Count(&cashCount)
	if cashCount != 0 {
		t.Errorf("un pago digital no debe generar ningún movimiento de efectivo, got %d", cashCount)
	}
}
