package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Fase 2 — CxP en GetSessionReport: una compra a crédito registrada en la sesión A debe mostrarse
// ahí como "CxP generada" (informativo, sin afectar el físico); un pago a proveedor no-efectivo
// hecho DESPUÉS en la sesión B (distinta) debe aparecer en el reporte de B, no en el de A — misma
// separación documento/pago que P0 estableció para CxC.

func setupSessionReportPayableTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantCashSession{}, &database.TenantCashMovement{},
		&database.TenantPaymentMethod{}, &database.TenantBankAccount{}, &database.TenantBankMovement{},
		&database.TenantSale{}, &database.TenantSalePayment{},
		&database.TenantPurchase{}, &database.TenantPurchasePayable{}, &database.TenantPurchasePayment{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestGetSessionReport_compraACredito_yPagoEnOtraSesion(t *testing.T) {
	db := setupSessionReportPayableTestDB(t)
	svc := NewCashBankService(db)

	sessionA := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(sessionA).Error; err != nil {
		t.Fatal(err)
	}
	sessionB := &database.TenantCashSession{BranchID: 1, UserID: 2, OpenedBy: 2, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(sessionB).Error; err != nil {
		t.Fatal(err)
	}

	// Compra a crédito registrada en sesión A.
	purchase := &database.TenantPurchase{
		ID: 1, BranchID: 1, UserID: 1, CashSessionID: &sessionA.ID,
		DocType: "factura", Series: "F001", Number: "1", IssueDate: time.Now(),
		Total: 5000, PaymentMethod: "", Status: "received", CreatedAt: time.Now(),
	}
	if err := db.Create(purchase).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPurchasePayable{
		PurchaseID: purchase.ID, OriginalAmount: 5000, PaidAmount: 0, Status: "pending",
	}).Error; err != nil {
		t.Fatal(err)
	}

	// Pago a proveedor por Yape, hecho DESPUÉS, en la sesión B.
	if err := db.Create(&database.TenantPurchasePayment{
		PurchaseID: purchase.ID, Method: "yape", Amount: 1000, CashSessionID: &sessionB.ID, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	yapeAcc := &database.TenantBankAccount{Name: "Yape", Type: "wallet", Active: true}
	if err := db.Create(yapeAcc).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantBankMovement{
		BankAccountID: yapeAcc.ID, Type: "debit", Amount: 1000, Date: time.Now(),
		PurchaseID: &purchase.ID, CashSessionID: &sessionB.ID, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	reportA, err := svc.GetSessionReport(sessionA.ID)
	if err != nil {
		t.Fatal(err)
	}
	reportB, err := svc.GetSessionReport(sessionB.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Sesión A: la compra a crédito aparece como CxP generada, no como gasto.
	if reportA.PayableGenerated.Total != 5000 {
		t.Errorf("reportA.PayableGenerated.Total = %v, want 5000", reportA.PayableGenerated.Total)
	}
	if reportA.Totals.TotalPurchases != 0 {
		t.Errorf("reportA.Totals.TotalPurchases = %v, want 0 (nada se pagó en la sesión A)", reportA.Totals.TotalPurchases)
	}
	for _, row := range reportA.ExpenseDetail {
		if row.Amount == 5000 {
			t.Errorf("la compra a crédito no debe aparecer en ExpenseDetail de A: %+v", row)
		}
	}

	// Sesión B: el pago a proveedor aparece como gasto (compra), con su método correcto — y la
	// compra en sí (CxP generada) NO aparece aquí, porque se registró en A, no en B.
	if reportB.PayableGenerated.Total != 0 {
		t.Errorf("reportB.PayableGenerated.Total = %v, want 0", reportB.PayableGenerated.Total)
	}
	if reportB.Totals.TotalPurchases != 1000 {
		t.Errorf("reportB.Totals.TotalPurchases = %v, want 1000", reportB.Totals.TotalPurchases)
	}
	found := false
	for _, mt := range reportB.TotalsByMethod.Purchases {
		if mt.Method == "yape" && mt.Total == 1000 {
			found = true
		}
	}
	if !found {
		t.Errorf("reportB.TotalsByMethod.Purchases no trae yape=1000: %+v", reportB.TotalsByMethod.Purchases)
	}

	// Ninguna de las dos sesiones ve su efectivo físico afectado (todo es crédito/Yape).
	if reportA.CashPhysical.PhysicalBalance != 0 || reportB.CashPhysical.PhysicalBalance != 0 {
		t.Errorf("PhysicalBalance debería ser 0 en ambas: A=%v B=%v", reportA.CashPhysical.PhysicalBalance, reportB.CashPhysical.PhysicalBalance)
	}

	// El movimiento bancario del pago CxP debe clasificarse como "pago_proveedor", no "compra"
	// (gap 2 de la revisión funcional): TenantPurchase.PaymentMethod == "" identifica sin
	// ambigüedad que CUALQUIER movimiento de esa compra es un pago a proveedor, nunca el pago
	// inmediato (Decisión A prohíbe la compra mixta).
	foundPagoProveedor := false
	for _, row := range reportB.ExpenseDetail {
		if row.Amount == 1000 {
			if row.Type != "pago_proveedor" {
				t.Errorf("el pago CxP en reportB.ExpenseDetail tiene Type=%q, want pago_proveedor", row.Type)
			}
			foundPagoProveedor = true
		}
	}
	if !foundPagoProveedor {
		t.Fatal("no se encontró la fila del pago CxP en reportB.ExpenseDetail")
	}
}

// Gap 2 (revisión funcional) — lado EFECTIVO: una compra a crédito (payment_method vacío) que
// después recibe un pago a proveedor EN EFECTIVO debe clasificarse como "pago_proveedor" en
// GetSessionReport, no como "compra" — antes de esta corrección, cualquier TenantCashMovement con
// PurchaseID se etiquetaba siempre "compra" sin importar su origen.
func TestGetSessionReport_pagoCxPEnEfectivo_seClasificaComoPagoProveedor(t *testing.T) {
	db := setupSessionReportPayableTestDB(t)
	svc := NewCashBankService(db)
	if err := db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true, DestinationType: "cash"}).Error; err != nil {
		t.Fatal(err)
	}

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 100, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}

	// Compra a crédito (payment_method vacío) — registrada sin pago inmediato.
	purchase := &database.TenantPurchase{
		ID: 1, BranchID: 1, UserID: 1, CashSessionID: &session.ID,
		DocType: "factura", Series: "F001", Number: "1", IssueDate: time.Now(),
		Total: 300, PaymentMethod: "", Status: "received", CreatedAt: time.Now(),
	}
	if err := db.Create(purchase).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPurchasePayable{
		PurchaseID: purchase.ID, OriginalAmount: 300, PaidAmount: 0, Status: "pending",
	}).Error; err != nil {
		t.Fatal(err)
	}

	// Pago a proveedor EN EFECTIVO (equivalente a lo que crearía PayableService.Pay vía
	// RecordExpensePayment): un TenantPurchasePayment + su TenantCashMovement.
	if err := db.Create(&database.TenantPurchasePayment{
		PurchaseID: purchase.ID, Method: "cash", Amount: 300, CashSessionID: &session.ID, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	docNumber := purchase.Series + "-" + purchase.Number
	if err := svc.RecordExpensePayment(db, "cash", 300, &session.ID, docNumber, "Pago proveedor "+docNumber, &purchase.ID, 1); err != nil {
		t.Fatal(err)
	}

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range report.ExpenseDetail {
		if row.Amount == 300 {
			if row.Type != "pago_proveedor" {
				t.Errorf("Type = %q, want pago_proveedor (compra a crédito, PaymentMethod vacío)", row.Type)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no se encontró el pago CxP en efectivo en ExpenseDetail")
	}
	// El pago en efectivo SÍ afecta el físico (a diferencia del bancario del test anterior).
	if report.CashPhysical.PhysicalBalance != -200 { // 100 apertura - 300 pago
		t.Errorf("PhysicalBalance = %v, want -200", report.CashPhysical.PhysicalBalance)
	}
}
