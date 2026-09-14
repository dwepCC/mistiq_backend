package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Gap 1 (revisión funcional post-Fase2): ListMovementsReport (reporte multi-sesión) nunca incluía
// los movimientos bancarios reales de compras/CxP — solo tenant_cash_movements (efectivo) y
// tenant_sale_payments (ventas). buildPurchasePaymentMovementRows corrige esto leyendo
// TenantPurchase (compra contado no-efectivo) y TenantPurchasePayment (pago CxP, Fase 2)
// directamente — mismas dos fuentes que ya usa GetSessionReport para una sola sesión, ahora
// filtradas por MovementReportFilters en vez de un sessionID fijo.

func setupMovementsReportPurchaseTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantCashSession{}, &database.TenantCashMovement{},
		&database.TenantPaymentMethod{}, &database.TenantBankAccount{}, &database.TenantBankMovement{},
		&database.TenantBranch{}, &database.TenantUser{}, &database.TenantContact{},
		&database.TenantSale{}, &database.TenantSalePayment{},
		&database.TenantPurchase{}, &database.TenantPurchasePayable{}, &database.TenantPurchasePayment{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// Compra al contado por Yape: antes invisible en ListMovementsReport, ahora debe aparecer como
// "compra", canal "electronic" (nunca "cash" — evita mezclar banco con el físico), con su propia
// cash_session_id, y sin duplicarse (no debe aparecer también en el canal cash).
func TestListMovementsReport_compraContadoYape_apareceEnElectronic(t *testing.T) {
	db := setupMovementsReportPurchaseTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	purchase := &database.TenantPurchase{
		ID: 1, BranchID: 1, UserID: 1, CashSessionID: &session.ID,
		DocType: "factura", Series: "F001", Number: "1", IssueDate: time.Now(),
		Total: 120, PaymentMethod: "yape", Status: "received", CreatedAt: time.Now(),
	}
	if err := db.Create(purchase).Error; err != nil {
		t.Fatal(err)
	}

	split, err := svc.ListMovementsReport(MovementReportFilters{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(split.Cash.Data) != 0 {
		t.Errorf("canal cash trae %d filas, want 0 (la compra es Yape, no debe caer en efectivo)", len(split.Cash.Data))
	}
	if len(split.Electronic.Data) != 1 {
		t.Fatalf("canal electronic trae %d filas, want 1: %+v", len(split.Electronic.Data), split.Electronic.Data)
	}
	row := split.Electronic.Data[0]
	if row.Type != "compra" {
		t.Errorf("Type = %q, want compra", row.Type)
	}
	if row.PaymentMethod != "yape" {
		t.Errorf("PaymentMethod = %q, want yape", row.PaymentMethod)
	}
	if row.CashSessionID != session.ID {
		t.Errorf("CashSessionID = %v, want %v", row.CashSessionID, session.ID)
	}
	if row.Amount != -120 {
		t.Errorf("Amount = %v, want -120 (egreso)", row.Amount)
	}
}

// Pago a proveedor (CxP) por transferencia, en una sesión DISTINTA a la de registro de la compra:
// debe aparecer como "pago_proveedor" en el canal electronic, con la sesión del PAGO (no la de la
// compra) — misma separación documento/pago que P0 estableció para CxC.
func TestListMovementsReport_pagoCxPTransferencia_apareceConSuPropiaSesion(t *testing.T) {
	db := setupMovementsReportPurchaseTestDB(t)
	svc := NewCashBankService(db)

	sessionRegistro := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(sessionRegistro).Error; err != nil {
		t.Fatal(err)
	}
	sessionPago := &database.TenantCashSession{BranchID: 1, UserID: 2, OpenedBy: 2, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(sessionPago).Error; err != nil {
		t.Fatal(err)
	}

	purchase := &database.TenantPurchase{
		ID: 1, BranchID: 1, UserID: 1, CashSessionID: &sessionRegistro.ID,
		DocType: "factura", Series: "F001", Number: "1", IssueDate: time.Now(),
		Total: 500, PaymentMethod: "", Status: "received", CreatedAt: time.Now(),
	}
	if err := db.Create(purchase).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPurchasePayment{
		PurchaseID: purchase.ID, Method: "transferencia", Amount: 500, CashSessionID: &sessionPago.ID, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	// Filtro por la sesión de REGISTRO: el pago (en otra sesión) no debe aparecer aquí.
	splitRegistro, err := svc.ListMovementsReport(MovementReportFilters{SessionID: sessionRegistro.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(splitRegistro.Electronic.Data) != 0 {
		t.Errorf("sesión de registro: electronic trae %d filas, want 0 (el pago ocurrió en otra sesión)", len(splitRegistro.Electronic.Data))
	}

	// Filtro por la sesión del PAGO: debe aparecer, clasificado pago_proveedor.
	splitPago, err := svc.ListMovementsReport(MovementReportFilters{SessionID: sessionPago.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(splitPago.Electronic.Data) != 1 {
		t.Fatalf("sesión de pago: electronic trae %d filas, want 1: %+v", len(splitPago.Electronic.Data), splitPago.Electronic.Data)
	}
	row := splitPago.Electronic.Data[0]
	if row.Type != "pago_proveedor" {
		t.Errorf("Type = %q, want pago_proveedor", row.Type)
	}
	if row.CashSessionID != sessionPago.ID {
		t.Errorf("CashSessionID = %v, want %v (la sesión del pago, no la de la compra)", row.CashSessionID, sessionPago.ID)
	}
}

// Compra en EFECTIVO: sigue viniendo de tenant_cash_movements (buildCashMovementReportRows), no
// de buildPurchasePaymentMovementRows — no debe duplicarse entre ambas fuentes.
func TestListMovementsReport_compraEfectivo_noSeDuplica(t *testing.T) {
	db := setupMovementsReportPurchaseTestDB(t)
	svc := NewCashBankService(db)

	if err := db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true, DestinationType: "cash"}).Error; err != nil {
		t.Fatal(err)
	}
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	purchase := &database.TenantPurchase{
		ID: 1, BranchID: 1, UserID: 1, CashSessionID: &session.ID,
		DocType: "factura", Series: "F001", Number: "1", IssueDate: time.Now(),
		Total: 80, PaymentMethod: "cash", Status: "received", CreatedAt: time.Now(),
	}
	if err := db.Create(purchase).Error; err != nil {
		t.Fatal(err)
	}
	docNumber := purchase.Series + "-" + purchase.Number
	if err := svc.RecordExpensePayment(db, "cash", 80, &session.ID, docNumber, "Compra "+docNumber, &purchase.ID, 1); err != nil {
		t.Fatal(err)
	}

	split, err := svc.ListMovementsReport(MovementReportFilters{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(split.Electronic.Data) != 0 {
		t.Errorf("electronic trae %d filas, want 0 (la compra es en efectivo)", len(split.Electronic.Data))
	}
	if len(split.Cash.Data) != 1 {
		t.Fatalf("cash trae %d filas, want exactamente 1 (sin duplicar): %+v", len(split.Cash.Data), split.Cash.Data)
	}
	if split.Cash.Data[0].Type != "compra" {
		t.Errorf("Type = %q, want compra", split.Cash.Data[0].Type)
	}
}
