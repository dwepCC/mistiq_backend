package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Fase 2 — CxP (cuentas por pagar), simétrico a internal/receivables/service (CxC). Estos tests
// prueban exactamente los escenarios especificados: compra a crédito sin pago, con adelanto
// (efectivo y digital), pagos posteriores en Cajas distintas a la de registro, pagos parciales,
// validaciones (sobre-pago, sin sesión, compra anulada), y que Purchase.CashSessionID JAMÁS
// cambia por un pago — igual que ya se probó y se corrigió para ventas/CxC en P0.

func setupPayableServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantPurchase{}, &database.TenantPurchasePayable{}, &database.TenantPurchasePayment{},
		&database.TenantCashSession{}, &database.TenantCashMovement{},
		&database.TenantBankAccount{}, &database.TenantBankMovement{}, &database.TenantPaymentMethod{},
		&database.TenantContact{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true, DestinationType: "cash"}).Error; err != nil {
		t.Fatal(err)
	}
	yapeAcc := &database.TenantBankAccount{Name: "Yape", Type: "wallet", Active: true}
	if err := db.Create(yapeAcc).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "yape", Name: "Yape", BankAccountID: &yapeAcc.ID, Active: true, DestinationType: "bank_account"}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func openPayableSession(t *testing.T, db *gorm.DB, sessionID, branchID, userID uint) {
	t.Helper()
	if err := db.Create(&database.TenantCashSession{
		ID: sessionID, BranchID: branchID, UserID: userID, OpenedBy: userID, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// seedCreditPurchase crea una compra a crédito (PaymentMethod vacío) + su TenantPurchasePayable —
// exactamente lo que purchase_service.Create hace ahora para PaymentMethod=="" && total>0.
func seedCreditPurchase(t *testing.T, db *gorm.DB, branchID, registrationSessionID uint, total float64) *database.TenantPurchase {
	t.Helper()
	purchase := &database.TenantPurchase{
		BranchID: branchID, UserID: 1, CashSessionID: &registrationSessionID,
		DocType: "factura", Series: "F001", Number: "00000001",
		IssueDate: time.Now(), Total: total, Status: "received",
	}
	if err := db.Create(purchase).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPurchasePayable{
		PurchaseID: purchase.ID, OriginalAmount: total, PaidAmount: 0, Status: StatusPending,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return purchase
}

func getPayable(t *testing.T, db *gorm.DB, purchaseID uint) database.TenantPurchasePayable {
	t.Helper()
	var p database.TenantPurchasePayable
	if err := db.Where("purchase_id = ?", purchaseID).First(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// TEST 1 — Compra crédito sin pago.
func TestPay_compraCreditoSinPago_generaCxPSinMovimientos(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	purchase := seedCreditPurchase(t, db, 1, 20, 5000)

	if purchase.CashSessionID == nil || *purchase.CashSessionID != 20 {
		t.Fatalf("purchase.CashSessionID = %v, want 20", purchase.CashSessionID)
	}
	payable := getPayable(t, db, purchase.ID)
	paid, due := PayableBalance(payable)
	if paid != 0 || due != 5000 {
		t.Fatalf("paid=%v due=%v, want paid=0 due=5000", paid, due)
	}
	var paymentCount, cashCount, bankCount int64
	db.Model(&database.TenantPurchasePayment{}).Where("purchase_id = ?", purchase.ID).Count(&paymentCount)
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", purchase.ID).Count(&cashCount)
	db.Model(&database.TenantBankMovement{}).Where("purchase_id = ?", purchase.ID).Count(&bankCount)
	if paymentCount != 0 || cashCount != 0 || bankCount != 0 {
		t.Errorf("no debe existir ningún pago/movimiento: payments=%d cash=%d bank=%d", paymentCount, cashCount, bankCount)
	}
}

// TEST 2 — Compra crédito con adelanto EFECTIVO, en la misma Caja de registro.
func TestPay_adelantoEfectivo_mismaCajaDeRegistro(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	openPayableSession(t, db, 20, 1, 1)
	purchase := seedCreditPurchase(t, db, 1, 20, 5000)

	svc := NewPayableService(db)
	if err := svc.Pay(purchase.ID, PayInput{Amount: 1000, Method: "cash", UserID: 1}); err != nil {
		t.Fatalf("Pay: %v", err)
	}

	var reloaded database.TenantPurchase
	db.First(&reloaded, purchase.ID)
	if reloaded.CashSessionID == nil || *reloaded.CashSessionID != 20 {
		t.Fatalf("purchase.CashSessionID = %v, want 20 (no debe cambiar)", reloaded.CashSessionID)
	}

	var payment database.TenantPurchasePayment
	if err := db.Where("purchase_id = ?", purchase.ID).First(&payment).Error; err != nil {
		t.Fatal(err)
	}
	if payment.CashSessionID == nil || *payment.CashSessionID != 20 || payment.Amount != 1000 {
		t.Fatalf("payment inesperado: %+v", payment)
	}

	var cashMov database.TenantCashMovement
	if err := db.Where("purchase_id = ?", purchase.ID).First(&cashMov).Error; err != nil {
		t.Fatal(err)
	}
	if cashMov.CashSessionID != 20 || cashMov.Amount != 1000 {
		t.Fatalf("cash movement inesperado: %+v", cashMov)
	}

	payable := getPayable(t, db, purchase.ID)
	_, due := PayableBalance(payable)
	if due != 4000 {
		t.Errorf("CxP pendiente = %v, want 4000", due)
	}
	if payable.Status != StatusPartial {
		t.Errorf("status = %q, want partial", payable.Status)
	}
}

// TEST 3 — Compra crédito con pago DIGITAL (Yape): genera BankMovement, no CashMovement.
func TestPay_pagoDigital_generaBankMovementSinEfectivo(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	openPayableSession(t, db, 20, 1, 1)
	purchase := seedCreditPurchase(t, db, 1, 20, 5000)

	svc := NewPayableService(db)
	if err := svc.Pay(purchase.ID, PayInput{Amount: 1000, Method: "yape", UserID: 1}); err != nil {
		t.Fatalf("Pay: %v", err)
	}

	var bankMov database.TenantBankMovement
	if err := db.Where("purchase_id = ?", purchase.ID).First(&bankMov).Error; err != nil {
		t.Fatal(err)
	}
	if bankMov.CashSessionID == nil || *bankMov.CashSessionID != 20 || bankMov.Amount != 1000 {
		t.Fatalf("bank movement inesperado: %+v", bankMov)
	}
	var cashCount int64
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", purchase.ID).Count(&cashCount)
	if cashCount != 0 {
		t.Errorf("no debe existir ningún cash movement para un pago Yape, got %d", cashCount)
	}
}

// TEST 4 — Pago posterior en OTRA Caja: la compra no cambia, el pago sí tiene su propia Caja.
func TestPay_pagoPosteriorEnOtraCaja_compraConservaSuSesion(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	openPayableSession(t, db, 30, 1, 2)
	purchase := seedCreditPurchase(t, db, 1, 20, 5000) // registrada en Caja 20 (sin fila TenantCashSession — no hace falta, Void/Pay no la validan)

	svc := NewPayableService(db)
	sess30 := uint(30)
	if err := svc.Pay(purchase.ID, PayInput{Amount: 2000, Method: "cash", CashSessionID: &sess30, UserID: 2}); err != nil {
		t.Fatalf("Pay: %v", err)
	}

	var reloaded database.TenantPurchase
	db.First(&reloaded, purchase.ID)
	if reloaded.CashSessionID == nil || *reloaded.CashSessionID != 20 {
		t.Fatalf("purchase.CashSessionID = %v, want 20", reloaded.CashSessionID)
	}
	var payment database.TenantPurchasePayment
	db.Where("purchase_id = ?", purchase.ID).First(&payment)
	if payment.CashSessionID == nil || *payment.CashSessionID != 30 {
		t.Fatalf("payment.CashSessionID = %v, want 30", payment.CashSessionID)
	}
	var cashMov database.TenantCashMovement
	db.Where("purchase_id = ?", purchase.ID).First(&cashMov)
	if cashMov.CashSessionID != 30 {
		t.Fatalf("cash_movement.CashSessionID = %v, want 30", cashMov.CashSessionID)
	}
}

// TEST 5 — Múltiples pagos en Cajas diferentes: saldo correcto, cada pago con su propia Caja.
func TestPay_multiplesPagosEnCajasDistintas(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	openPayableSession(t, db, 30, 1, 2)
	openPayableSession(t, db, 35, 1, 3)
	purchase := seedCreditPurchase(t, db, 1, 20, 5000)

	svc := NewPayableService(db)
	sess30, sess35 := uint(30), uint(35)
	if err := svc.Pay(purchase.ID, PayInput{Amount: 2000, Method: "cash", CashSessionID: &sess30, UserID: 2}); err != nil {
		t.Fatalf("pago 1: %v", err)
	}
	if err := svc.Pay(purchase.ID, PayInput{Amount: 1000, Method: "cash", CashSessionID: &sess35, UserID: 3}); err != nil {
		t.Fatalf("pago 2: %v", err)
	}

	var reloaded database.TenantPurchase
	db.First(&reloaded, purchase.ID)
	if reloaded.CashSessionID == nil || *reloaded.CashSessionID != 20 {
		t.Fatalf("purchase.CashSessionID = %v, want 20", reloaded.CashSessionID)
	}
	var payments []database.TenantPurchasePayment
	db.Where("purchase_id = ?", purchase.ID).Order("id ASC").Find(&payments)
	if len(payments) != 2 {
		t.Fatalf("payments: got %d, want 2", len(payments))
	}
	if payments[0].CashSessionID == nil || *payments[0].CashSessionID != 30 {
		t.Errorf("payment1.CashSessionID = %v, want 30", payments[0].CashSessionID)
	}
	if payments[1].CashSessionID == nil || *payments[1].CashSessionID != 35 {
		t.Errorf("payment2.CashSessionID = %v, want 35", payments[1].CashSessionID)
	}
	payable := getPayable(t, db, purchase.ID)
	_, due := PayableBalance(payable)
	if due != 2000 {
		t.Errorf("saldo = %v, want 2000", due)
	}
}

// TEST 6/7 — Pago parcial y varias aplicaciones hasta saldar por completo: importe/pagado/saldo/
// estado correctos en cada paso, terminando en PAGADO.
func TestPay_pagosParciales_hastaSaldarCompleto(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	openPayableSession(t, db, 20, 1, 1)
	purchase := seedCreditPurchase(t, db, 1, 20, 5000)
	svc := NewPayableService(db)

	if err := svc.Pay(purchase.ID, PayInput{Amount: 1500, Method: "cash", UserID: 1}); err != nil {
		t.Fatal(err)
	}
	p := getPayable(t, db, purchase.ID)
	if paid, due := PayableBalance(p); paid != 1500 || due != 3500 || p.Status != StatusPartial {
		t.Fatalf("tras pago 1: paid=%v due=%v status=%q", paid, due, p.Status)
	}

	if err := svc.Pay(purchase.ID, PayInput{Amount: 1000, Method: "cash", UserID: 1}); err != nil {
		t.Fatal(err)
	}
	p = getPayable(t, db, purchase.ID)
	if paid, due := PayableBalance(p); paid != 2500 || due != 2500 || p.Status != StatusPartial {
		t.Fatalf("tras pago 2: paid=%v due=%v status=%q", paid, due, p.Status)
	}

	if err := svc.Pay(purchase.ID, PayInput{Amount: 2500, Method: "cash", UserID: 1}); err != nil {
		t.Fatal(err)
	}
	p = getPayable(t, db, purchase.ID)
	if paid, due := PayableBalance(p); paid != 5000 || due != 0 || p.Status != StatusPaid {
		t.Fatalf("tras pago 3: paid=%v due=%v status=%q, want paid=5000 due=0 status=paid", paid, due, p.Status)
	}
}

// TEST 8 — Pago mayor al saldo: debe rechazarse.
func TestPay_montoMayorAlSaldo_rechaza(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	openPayableSession(t, db, 20, 1, 1)
	purchase := seedCreditPurchase(t, db, 1, 20, 5000)
	svc := NewPayableService(db)

	if err := svc.Pay(purchase.ID, PayInput{Amount: 6000, Method: "cash", UserID: 1}); err == nil {
		t.Fatal("se esperaba error: el pago supera el saldo pendiente")
	}
	var count int64
	db.Model(&database.TenantPurchasePayment{}).Where("purchase_id = ?", purchase.ID).Count(&count)
	if count != 0 {
		t.Errorf("no debió crearse ningún pago, got %d", count)
	}
}

// TEST 9 — Pago en efectivo SIN Caja abierta: debe rechazarse.
func TestPay_efectivoSinCajaAbierta_rechaza(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	purchase := seedCreditPurchase(t, db, 1, 20, 5000)
	svc := NewPayableService(db)

	if err := svc.Pay(purchase.ID, PayInput{Amount: 1000, Method: "cash", UserID: 9}); err == nil {
		t.Fatal("se esperaba error: pago en efectivo sin sesión de caja abierta")
	}
}

// TEST 10 — Pago DIGITAL sin Caja abierta: debe rechazarse igual que efectivo (P0: cualquier
// método exige sesión).
func TestPay_digitalSinCajaAbierta_rechaza(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	purchase := seedCreditPurchase(t, db, 1, 20, 5000)
	svc := NewPayableService(db)

	if err := svc.Pay(purchase.ID, PayInput{Amount: 1000, Method: "yape", UserID: 9}); err == nil {
		t.Fatal("se esperaba error: pago por Yape sin sesión de caja abierta")
	}
}

// TEST 11 — Compra anulada: no debe poder recibir un pago.
func TestPay_compraAnulada_rechaza(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	openPayableSession(t, db, 20, 1, 1)
	purchase := seedCreditPurchase(t, db, 1, 20, 5000)
	db.Model(purchase).Update("status", "cancelled")
	svc := NewPayableService(db)

	if err := svc.Pay(purchase.ID, PayInput{Amount: 1000, Method: "cash", UserID: 1}); err == nil {
		t.Fatal("se esperaba error: no se puede pagar una compra anulada")
	}
}

// Hallazgo #2 (revisión con MySQL real): List() armaba purchase_number con
// `p.series || '-' || p.number` — concatenación válida en SQLite (por eso este mismo test, antes
// de la corrección, ya pasaba aquí) pero `||` es el operador lógico OR en MySQL, así que en
// producción el campo mostraba "1" en vez de "F001-00000001". La corrección selecciona series y
// number por separado y concatena en Go — esta prueba fija el formato esperado para que una
// regresión futura (volver a concatenar en SQL) se detecte incluso en SQLite.
func TestList_purchaseNumber_seriesGuionNumero(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	openPayableSession(t, db, 20, 1, 1)
	seedCreditPurchase(t, db, 1, 20, 118)
	svc := NewPayableService(db)

	rows, total, err := svc.List(ListFilter{BranchID: 1, Status: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("List() = %d filas (total=%d), want 1", len(rows), total)
	}
	if rows[0].PurchaseNumber != "F001-00000001" {
		t.Errorf("PurchaseNumber = %q, want %q", rows[0].PurchaseNumber, "F001-00000001")
	}
}
