package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Verificación a nivel de reporte de la corrección de trazabilidad: ahora que purchase_service
// .Create resuelve y exige cash_session_id para toda compra con pago inmediato (vínculo directo,
// no por sucursal+fecha), GetSessionReport(sesión A) debe mostrar únicamente las compras de A —
// nunca las de la sesión B, aunque ambas estén abiertas a la vez en la misma sucursal — y sin
// doble conteo.

func setupTwoCashiersReportTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantCashSession{}, &database.TenantCashMovement{},
		&database.TenantPaymentMethod{}, &database.TenantBankAccount{}, &database.TenantBankMovement{},
		&database.TenantSale{}, &database.TenantSalePayment{}, &database.TenantPurchase{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// createLinkedNonCashPurchase reproduce el camino real POST-corrección: purchase_service.Create
// ahora resuelve la sesión (ResolveCashSessionForPurchase) y la persiste directamente en
// TenantPurchase.CashSessionID — a diferencia del helper createNonCashPurchase de
// session_report_noncash_purchases_test.go (que sigue existiendo para simular el caso histórico,
// pre-corrección, de cash_session_id NULL).
func createLinkedNonCashPurchase(t *testing.T, db *gorm.DB, svc *CashBankService, purchaseID, sessionID, branchID, userID uint, method string, amount float64) {
	t.Helper()
	purchase := &database.TenantPurchase{
		ID: purchaseID, BranchID: branchID, UserID: userID, DocType: "factura",
		Series: "F001", Number: fmt.Sprintf("%d", purchaseID), IssueDate: time.Now(),
		Total: amount, PaymentMethod: method, Status: "received", CashSessionID: &sessionID, CreatedAt: time.Now(),
	}
	if err := db.Create(purchase).Error; err != nil {
		t.Fatal(err)
	}
	docNumber := purchase.Series + "-" + purchase.Number
	if err := svc.RecordExpensePayment(db, method, amount, &sessionID, docNumber, "Compra "+docNumber, &purchaseID, userID); err != nil {
		t.Fatal(err)
	}
}

func TestGetSessionReport_dosCajeros_soloMuestraLasComprasDeSuPropiaSesion(t *testing.T) {
	db := setupTwoCashiersReportTestDB(t)
	svc := NewCashBankService(db)

	sessionA := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(sessionA).Error; err != nil {
		t.Fatal(err)
	}
	sessionB := &database.TenantCashSession{BranchID: 1, UserID: 2, OpenedBy: 2, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(sessionB).Error; err != nil {
		t.Fatal(err)
	}

	createLinkedNonCashPurchase(t, db, svc, 901, sessionA.ID, 1, 1, "yape", 70)
	createLinkedNonCashPurchase(t, db, svc, 902, sessionB.ID, 1, 2, "transferencia", 130)

	reportA, err := svc.GetSessionReport(sessionA.ID)
	if err != nil {
		t.Fatal(err)
	}
	reportB, err := svc.GetSessionReport(sessionB.ID)
	if err != nil {
		t.Fatal(err)
	}

	purchasesA := map[string]float64{}
	for _, mt := range reportA.TotalsByMethod.Purchases {
		purchasesA[mt.Method] = mt.Total
	}
	if purchasesA["yape"] != 70 {
		t.Errorf("reporte de sesión A: TotalsByMethod.Purchases[yape] = %v, want 70", purchasesA["yape"])
	}
	if _, ok := purchasesA["transferencia"]; ok {
		t.Errorf("reporte de sesión A no debe traer la compra de la sesión B (transferencia): %+v", reportA.TotalsByMethod.Purchases)
	}

	purchasesB := map[string]float64{}
	for _, mt := range reportB.TotalsByMethod.Purchases {
		purchasesB[mt.Method] = mt.Total
	}
	if purchasesB["transferencia"] != 130 {
		t.Errorf("reporte de sesión B: TotalsByMethod.Purchases[transferencia] = %v, want 130", purchasesB["transferencia"])
	}
	if _, ok := purchasesB["yape"]; ok {
		t.Errorf("reporte de sesión B no debe traer la compra de la sesión A (yape): %+v", reportB.TotalsByMethod.Purchases)
	}

	// Sin doble conteo: cada compra aparece exactamente una vez, en el reporte de SU sesión.
	compraRowsA := 0
	for _, row := range reportA.ExpenseDetail {
		if row.Type == "compra" {
			compraRowsA++
		}
	}
	if compraRowsA != 1 {
		t.Errorf("ExpenseDetail de sesión A trae %d filas de compra, want 1", compraRowsA)
	}
	compraRowsB := 0
	for _, row := range reportB.ExpenseDetail {
		if row.Type == "compra" {
			compraRowsB++
		}
	}
	if compraRowsB != 1 {
		t.Errorf("ExpenseDetail de sesión B trae %d filas de compra, want 1", compraRowsB)
	}

	// PhysicalBalance sigue siendo solo efectivo: ninguna de las dos compras (Yape/transferencia)
	// debe afectarlo en ninguna de las dos sesiones.
	if reportA.CashPhysical.PhysicalBalance != 0 {
		t.Errorf("PhysicalBalance sesión A = %v, want 0", reportA.CashPhysical.PhysicalBalance)
	}
	if reportB.CashPhysical.PhysicalBalance != 0 {
		t.Errorf("PhysicalBalance sesión B = %v, want 0", reportB.CashPhysical.PhysicalBalance)
	}
}
