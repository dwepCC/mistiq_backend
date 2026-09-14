package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Gap encontrado explorando Tukichef (misma clase que el de compras/CxP en
// movements_report_purchase_test.go): ListMovementsReport nunca leía tenant_bank_movements para
// movimientos MANUALES (AddMovement, sin sale_id/purchase_id). Desde el fix de la duplicación en
// AddMovement, un ingreso/egreso manual por método no efectivo vive EXCLUSIVAMENTE ahí — antes
// también generaba un TenantCashMovement, que sí cubría buildCashMovementReportRows. Sin
// buildManualBankMovementRows, ese movimiento desaparecía por completo de este reporte
// multisesión (usado por Tukifac y Tukichef por igual), aunque siguiera visible en el reporte de
// sesión individual (GetSessionReport, ya corregido antes).

func setupMovementsReportManualTestDB(t *testing.T) *gorm.DB {
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

// Ingreso manual por Yape: debe aparecer en el canal electronic como "ingreso", con su propia
// cash_session_id, categoría y referencia — nunca en el canal cash (evita mezclar banco con el
// físico, mismo criterio que ya aplica a compras/CxP no efectivo).
func TestListMovementsReport_ingresoManualYape_apareceEnElectronic(t *testing.T) {
	db := setupMovementsReportManualTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	acc := &database.TenantBankAccount{Name: "Yape", Type: "wallet", PaymentMethod: "yape", Active: true}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMovement(session.ID, 1, "income", "ingreso_manual", "ref-yape", "yape", 150, "nota", nil); err != nil {
		t.Fatal(err)
	}

	split, err := svc.ListMovementsReport(MovementReportFilters{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(split.Cash.Data) != 0 {
		t.Errorf("canal cash trae %d filas, want 0 (el ingreso es Yape, no debe caer en efectivo)", len(split.Cash.Data))
	}
	if len(split.Electronic.Data) != 1 {
		t.Fatalf("canal electronic trae %d filas, want 1: %+v", len(split.Electronic.Data), split.Electronic.Data)
	}
	row := split.Electronic.Data[0]
	if row.Type != "ingreso" {
		t.Errorf("Type = %q, want ingreso", row.Type)
	}
	if row.PaymentMethod != "yape" {
		t.Errorf("PaymentMethod = %q, want yape", row.PaymentMethod)
	}
	if row.CashSessionID != session.ID {
		t.Errorf("CashSessionID = %v, want %v", row.CashSessionID, session.ID)
	}
	if row.Amount != 150 {
		t.Errorf("Amount = %v, want 150 (ingreso)", row.Amount)
	}
	if row.DocNumber != "ref-yape" {
		t.Errorf("DocNumber (referencia) = %q, want ref-yape", row.DocNumber)
	}
}

// Egreso manual por transferencia: canal electronic, tipo "egreso", monto negativo.
func TestListMovementsReport_egresoManualTransferencia_apareceEnElectronic(t *testing.T) {
	db := setupMovementsReportManualTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	acc := &database.TenantBankAccount{Name: "Cta transferencias", Type: "bank", PaymentMethod: "transferencia", Active: true}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMovement(session.ID, 1, "expense", "pago_proveedor", "ref-transf", "transferencia", 200, "", nil); err != nil {
		t.Fatal(err)
	}

	split, err := svc.ListMovementsReport(MovementReportFilters{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(split.Electronic.Data) != 1 {
		t.Fatalf("canal electronic trae %d filas, want 1: %+v", len(split.Electronic.Data), split.Electronic.Data)
	}
	row := split.Electronic.Data[0]
	if row.Type != "egreso" {
		t.Errorf("Type = %q, want egreso", row.Type)
	}
	if row.Amount != -200 {
		t.Errorf("Amount = %v, want -200 (egreso)", row.Amount)
	}
}

// Ingreso/egreso manual en EFECTIVO: sigue viniendo de tenant_cash_movements
// (buildCashMovementReportRows), no de buildManualBankMovementRows — no debe duplicarse ni
// aparecer también en electronic.
func TestListMovementsReport_manualEfectivo_noSeDuplica(t *testing.T) {
	db := setupMovementsReportManualTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMovement(session.ID, 1, "income", "ingreso_manual", "ref-efectivo", "efectivo", 40, "", nil); err != nil {
		t.Fatal(err)
	}

	split, err := svc.ListMovementsReport(MovementReportFilters{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(split.Electronic.Data) != 0 {
		t.Errorf("electronic trae %d filas, want 0 (el ingreso es en efectivo)", len(split.Electronic.Data))
	}
	if len(split.Cash.Data) != 1 {
		t.Fatalf("cash trae %d filas, want exactamente 1 (sin duplicar): %+v", len(split.Cash.Data), split.Cash.Data)
	}
	if split.Cash.Data[0].Type != "ingreso" {
		t.Errorf("Type = %q, want ingreso", split.Cash.Data[0].Type)
	}
}

// Filtro por sesión: un manual no efectivo registrado en OTRA sesión no debe aparecer.
func TestListMovementsReport_manualNoEfectivo_filtraPorSesion(t *testing.T) {
	db := setupMovementsReportManualTestDB(t)
	svc := NewCashBankService(db)

	sessionA := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(sessionA).Error; err != nil {
		t.Fatal(err)
	}
	sessionB := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(sessionB).Error; err != nil {
		t.Fatal(err)
	}
	acc := &database.TenantBankAccount{Name: "Yape", Type: "wallet", PaymentMethod: "yape", Active: true}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMovement(sessionA.ID, 1, "income", "ingreso_manual", "ref", "yape", 60, "", nil); err != nil {
		t.Fatal(err)
	}

	split, err := svc.ListMovementsReport(MovementReportFilters{SessionID: sessionB.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(split.Electronic.Data) != 0 {
		t.Errorf("sesión B: electronic trae %d filas, want 0 (el ingreso ocurrió en la sesión A)", len(split.Electronic.Data))
	}
}
