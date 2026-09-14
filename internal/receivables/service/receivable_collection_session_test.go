package service

import (
	"fmt"
	"testing"
	"time"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Implementación P0 — separación documento/pago/movimiento: Collect() ya NO sobrescribe
// sale.CashSessionID (la Caja donde se REGISTRÓ la venta, inmutable) y ahora exige sesión de caja
// del usuario para CUALQUIER método de cobro, no solo efectivo (ResolveCashSessionForCollection).
// Cada TenantSalePayment guarda su propia cash_session_id — la Caja donde OCURRIÓ ESE cobro.

func setupReceivableCollectionSessionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantSale{}, &database.TenantSaleDetraccion{}, &database.TenantSalePayment{},
		&database.TenantSaleCreditInstallment{},
		&database.TenantCashSession{}, &database.TenantCashMovement{},
		&database.TenantBankAccount{}, &database.TenantBankMovement{}, &database.TenantPaymentMethod{},
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

func seedCreditSaleForCollection(t *testing.T, db *gorm.DB, branchID uint, registrationSessionID uint, total float64) *database.TenantSale {
	t.Helper()
	sale := &database.TenantSale{
		BranchID: branchID, UserID: 1, CashSessionID: &registrationSessionID,
		Number: "F001-00000001", DocType: "boleta", Total: total, Status: "credit",
	}
	if err := db.Create(sale).Error; err != nil {
		t.Fatal(err)
	}
	return sale
}

func openSessionFor(t *testing.T, db *gorm.DB, sessionID, branchID, userID uint) {
	t.Helper()
	if err := db.Create(&database.TenantCashSession{
		ID: sessionID, BranchID: branchID, UserID: userID, OpenedBy: userID, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// TEST 6 — CRÍTICO: venta registrada en Caja 25, cobrada después en Caja 30 (efectivo) y luego
// en Caja 35 (Yape). La venta NUNCA debe cambiar de Caja; cada pago y su movimiento conservan la
// suya propia.
func TestCollect_pagosPosteriores_ventaConservaSuSesionDeRegistro(t *testing.T) {
	db := setupReceivableCollectionSessionTestDB(t)
	sale := seedCreditSaleForCollection(t, db, 1, 25, 1000)
	openSessionFor(t, db, 30, 1, 2) // usuario 2, dueño de la Caja 30
	openSessionFor(t, db, 35, 1, 3) // usuario 3, dueño de la Caja 35

	svc := NewReceivableService(db)

	session30 := uint(30)
	if err := svc.Collect(sale.ID, CollectPaymentInput{
		Payments:      []salessvc.PaymentInput{{Method: "cash", Amount: 300}},
		CashSessionID: &session30,
		UserID:        2,
	}); err != nil {
		t.Fatalf("primer cobro (Caja 30, efectivo): %v", err)
	}

	session35 := uint(35)
	if err := svc.Collect(sale.ID, CollectPaymentInput{
		Payments:      []salessvc.PaymentInput{{Method: "yape", Amount: 300}},
		CashSessionID: &session35,
		UserID:        3,
	}); err != nil {
		t.Fatalf("segundo cobro (Caja 35, Yape): %v", err)
	}

	// La venta NUNCA cambia de Caja.
	var reloaded database.TenantSale
	if err := db.First(&reloaded, sale.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.CashSessionID == nil || *reloaded.CashSessionID != 25 {
		t.Fatalf("sale.CashSessionID = %v, want 25 (nunca debe cambiar)", reloaded.CashSessionID)
	}

	var payments []database.TenantSalePayment
	db.Where("sale_id = ?", sale.ID).Order("id ASC").Find(&payments)
	if len(payments) != 2 {
		t.Fatalf("TenantSalePayment: got %d, want 2", len(payments))
	}
	p1, p2 := payments[0], payments[1]
	if p1.Method != "cash" || p1.Amount != 300 || p1.CashSessionID == nil || *p1.CashSessionID != 30 {
		t.Fatalf("payment1 inesperado: %+v", p1)
	}
	if p2.Method != "yape" || p2.Amount != 300 || p2.CashSessionID == nil || *p2.CashSessionID != 35 {
		t.Fatalf("payment2 inesperado: %+v", p2)
	}

	var cashMov database.TenantCashMovement
	if err := db.Where("sale_id = ? AND payment_method = ?", sale.ID, "cash").First(&cashMov).Error; err != nil {
		t.Fatal(err)
	}
	if cashMov.CashSessionID != 30 || cashMov.Amount != 300 {
		t.Fatalf("cash movement inesperado: %+v", cashMov)
	}

	var bankMov database.TenantBankMovement
	if err := db.Where("sale_id = ?", sale.ID).First(&bankMov).Error; err != nil {
		t.Fatal(err)
	}
	if bankMov.CashSessionID == nil || *bankMov.CashSessionID != 35 || bankMov.Amount != 300 {
		t.Fatalf("bank movement inesperado: %+v", bankMov)
	}
}

// TEST 7 — Cobro posterior no-efectivo (Yape) SIN sesión de caja abierta del usuario: debe
// rechazarse, no puede quedar cash_session_id NULL.
func TestCollect_cobroNoEfectivo_sinSesionAbierta_rechaza(t *testing.T) {
	db := setupReceivableCollectionSessionTestDB(t)
	sale := seedCreditSaleForCollection(t, db, 1, 25, 1000)

	svc := NewReceivableService(db)
	err := svc.Collect(sale.ID, CollectPaymentInput{
		Payments: []salessvc.PaymentInput{{Method: "yape", Amount: 300}},
		UserID:   9, // sin ninguna sesión abierta
	})
	if err == nil {
		t.Fatal("se esperaba error: cobro por Yape sin sesión de caja abierta")
	}
	var count int64
	db.Model(&database.TenantSalePayment{}).Where("sale_id = ?", sale.ID).Count(&count)
	if count != 0 {
		t.Errorf("no debió crearse ningún pago, got %d", count)
	}
}

// TEST 8 — Cobro posterior en efectivo SIN sesión de caja abierta: debe rechazarse (ya exigido
// antes de esta implementación; se deja evidencia explícita).
func TestCollect_cobroEfectivo_sinSesionAbierta_rechaza(t *testing.T) {
	db := setupReceivableCollectionSessionTestDB(t)
	sale := seedCreditSaleForCollection(t, db, 1, 25, 1000)

	svc := NewReceivableService(db)
	err := svc.Collect(sale.ID, CollectPaymentInput{
		Payments: []salessvc.PaymentInput{{Method: "cash", Amount: 300}},
		UserID:   9,
	})
	if err == nil {
		t.Fatal("se esperaba error: cobro en efectivo sin sesión de caja abierta")
	}
	var count int64
	db.Model(&database.TenantSalePayment{}).Where("sale_id = ?", sale.ID).Count(&count)
	if count != 0 {
		t.Errorf("no debió crearse ningún pago, got %d", count)
	}
}
