package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Corrección del reporte antiguo (GetSessionReport / CashReportsPage.tsx / cashReportExcel.ts):
// CashPhysical.PhysicalBalance (y Totals.FinalBalance, del que se copia) sumaba TODOS los
// movimientos de tenant_cash_movements sin filtrar por método de pago — un ingreso/egreso manual
// por Yape/Plin/transferencia (que también queda en esa tabla para trazabilidad de sesión, igual
// que ya prueba record_payment_session_test.go) inflaba o desinflaba el "saldo físico esperado en
// efectivo". Estos tests reproducen exactamente el criterio ya correcto de
// getExpectedBalance/cashOnlyMovementTotals (IsCashPaymentMethod) y confirman que el reporte
// ahora coincide con él, sin dejar de listar los medios no-efectivo para trazabilidad/clasificación.

func setupCashReportPhysicalBalanceTestDB(t *testing.T) *gorm.DB {
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
		&database.TenantBranch{}, &database.TenantUser{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func seedReportPaymentMethods(t *testing.T, db *gorm.DB) {
	t.Helper()
	yapeAcc := &database.TenantBankAccount{Name: "Yape", Type: "wallet", Active: true}
	transfAcc := &database.TenantBankAccount{Name: "BCP", Type: "bank", Active: true}
	if err := db.Create(yapeAcc).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(transfAcc).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true, DestinationType: "cash"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "yape", Name: "Yape", BankAccountID: &yapeAcc.ID, Active: true, DestinationType: "bank_account"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "transferencia", Name: "Transferencia", BankAccountID: &transfAcc.ID, Active: true, DestinationType: "bank_account"}).Error; err != nil {
		t.Fatal(err)
	}
}

// createReportSale crea la venta + sus líneas de pago (tenant_sale_payments) y además llama a
// RecordPayment por cada línea — exactamente igual que sale_service.Create en producción — para
// que tanto el detalle de ventas (via TenantSalePayment) como el efectivo físico (via
// TenantCashMovement) queden poblados de forma realista.
func createReportSale(t *testing.T, db *gorm.DB, svc *CashBankService, saleID uint, sessionID uint, lines map[string]float64) {
	t.Helper()
	var total float64
	for _, amt := range lines {
		total += amt
	}
	sale := &database.TenantSale{
		ID: saleID, BranchID: 1, UserID: 1, CashSessionID: &sessionID,
		SeriesID: 1, DocType: "boleta", Series: "B001", Correlative: saleID,
		Number: fmt.Sprintf("B001-%d", saleID), IssueDate: time.Now(),
		Total: total, Status: "paid", CreatedAt: time.Now(),
	}
	if err := db.Create(sale).Error; err != nil {
		t.Fatal(err)
	}
	for method, amt := range lines {
		// CashSessionID: sale_service.Create() SIEMPRE la pobla en cada TenantSalePayment (P0).
		if err := db.Create(&database.TenantSalePayment{
			SaleID: saleID, Method: method, Amount: amt, CashSessionID: &sessionID, CreatedAt: time.Now(),
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := svc.RecordPayment(db, method, amt, &sessionID, sale.Number, "Venta "+sale.Number, &saleID, 1); err != nil {
			t.Fatal(err)
		}
	}
}

// 1. Sesión con efectivo únicamente: PhysicalBalance debe coincidir exactamente con
// getExpectedBalance, y no debe haber ninguna fila no-efectivo de por medio.
func TestGetSessionReport_cashOnly_physicalBalanceMatchesExpected(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 50, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createReportSale(t, db, svc, 1, session.ID, map[string]float64{"cash": 200})

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := svc.getExpectedBalance(session.ID)
	if want != 250 {
		t.Fatalf("getExpectedBalance = %v, want 250 (fixture inconsistente)", want)
	}
	if report.Totals.FinalBalance != want {
		t.Errorf("Totals.FinalBalance = %v, want %v", report.Totals.FinalBalance, want)
	}
	if report.CashPhysical.PhysicalBalance != want {
		t.Errorf("CashPhysical.PhysicalBalance = %v, want %v", report.CashPhysical.PhysicalBalance, want)
	}
}

// 2. Efectivo + Yape (dos ventas independientes): el Yape no debe tocar el físico, pero sigue
// disponible para trazabilidad/clasificación en Electronic y TotalsByMethod.Sales.
func TestGetSessionReport_cashPlusYape_physicalBalanceExcludesYape(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createReportSale(t, db, svc, 1, session.ID, map[string]float64{"cash": 100})
	createReportSale(t, db, svc, 2, session.ID, map[string]float64{"yape": 150})

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.CashPhysical.PhysicalBalance != 100 {
		t.Errorf("PhysicalBalance = %v, want 100 (solo la venta en efectivo)", report.CashPhysical.PhysicalBalance)
	}
	if report.Totals.FinalBalance != 100 {
		t.Errorf("Totals.FinalBalance = %v, want 100", report.Totals.FinalBalance)
	}
	// Trazabilidad: el Yape no desaparece, solo se excluye del físico.
	if report.Totals.TotalSalesCommercial != 250 {
		t.Errorf("TotalSalesCommercial = %v, want 250 (100 efectivo + 150 yape)", report.Totals.TotalSalesCommercial)
	}
	if report.Electronic.TotalSales != 150 {
		t.Errorf("Electronic.TotalSales = %v, want 150", report.Electronic.TotalSales)
	}
	foundYape := false
	for _, mt := range report.TotalsByMethod.Sales {
		if mt.Method == "yape" && mt.Total == 150 {
			foundYape = true
		}
	}
	if !foundYape {
		t.Errorf("TotalsByMethod.Sales no trae yape=150 para clasificación: %+v", report.TotalsByMethod.Sales)
	}
}

// 3. Efectivo + egreso manual por transferencia: el egreso no-efectivo no debe reducir el
// físico, pero sigue apareciendo en el detalle de egresos y en Movements para trazabilidad.
func TestGetSessionReport_cashPlusTransferExpense_physicalBalanceExcludesTransfer(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 500, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createReportSale(t, db, svc, 1, session.ID, map[string]float64{"cash": 300})
	if err := svc.AddMovement(session.ID, 1, "expense", "Pago proveedor", "REF-1", "transferencia", 200, "", nil); err != nil {
		t.Fatal(err)
	}

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := svc.getExpectedBalance(session.ID)
	if want != 800 {
		t.Fatalf("getExpectedBalance = %v, want 800 (fixture inconsistente)", want)
	}
	if report.CashPhysical.PhysicalBalance != want {
		t.Errorf("PhysicalBalance = %v, want %v (el egreso por transferencia no debe tocar el físico)", report.CashPhysical.PhysicalBalance, want)
	}
	// Trazabilidad: el egreso por transferencia sigue en el detalle y en Movements.
	foundExpenseRow := false
	for _, row := range report.ExpenseDetail {
		if row.PaymentMethod == "transferencia" && row.Amount == 200 {
			foundExpenseRow = true
		}
	}
	if !foundExpenseRow {
		t.Errorf("ExpenseDetail no lista el egreso por transferencia: %+v", report.ExpenseDetail)
	}
	foundMovement := false
	for _, mt := range report.TotalsByMethod.Movements {
		if mt.Method == "transferencia" && mt.Total == -200 {
			foundMovement = true
		}
	}
	if !foundMovement {
		t.Errorf("TotalsByMethod.Movements no trae transferencia=-200: %+v", report.TotalsByMethod.Movements)
	}
}

// 4. Venta mixta (una sola venta, tres métodos): PhysicalBalance debe reflejar solo la porción en
// efectivo, sin duplicar el detalle ni el total de la venta.
func TestGetSessionReport_mixedPaymentSale_physicalBalanceOnlyCash(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 50, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createReportSale(t, db, svc, 1, session.ID, map[string]float64{"cash": 100, "yape": 100, "transferencia": 100})

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.CashPhysical.PhysicalBalance != 150 { // 50 apertura + 100 efectivo
		t.Errorf("PhysicalBalance = %v, want 150", report.CashPhysical.PhysicalBalance)
	}
	if report.Totals.FinalBalance != 150 {
		t.Errorf("Totals.FinalBalance = %v, want 150", report.Totals.FinalBalance)
	}
	if report.Totals.TotalSalesCommercial != 300 {
		t.Errorf("TotalSalesCommercial = %v, want 300 (venta completa, los 3 métodos)", report.Totals.TotalSalesCommercial)
	}
	// El físico jamás debe coincidir con el total de la venta cuando hay métodos no-efectivo de
	// por medio — si coinciden, algo está sumando de más.
	if report.CashPhysical.PhysicalBalance == report.Totals.TotalSalesCommercial {
		t.Error("PhysicalBalance no debería igualar TotalSalesCommercial cuando hay yape/transferencia")
	}

	// Sin doble conteo: el detalle de ingresos de esta venta debe traer exactamente 3 filas
	// "venta" (una por línea de pago, vía TenantSalePayment) — la fila de TenantCashMovement de
	// la línea en efectivo NO debe generar una cuarta fila duplicada (ver "if m.SaleID != nil {
	// continue }" en GetSessionReport).
	ventaRows := 0
	var ventaSum float64
	for _, row := range report.IncomeDetail {
		if row.Type == "venta" {
			ventaRows++
			ventaSum += row.Amount
		}
	}
	if ventaRows != 3 {
		t.Errorf("IncomeDetail trae %d filas de venta, want 3 (sin duplicar la línea en efectivo)", ventaRows)
	}
	if ventaSum != 300 {
		t.Errorf("suma de IncomeDetail venta = %v, want 300 (100+100+100, sin doble conteo)", ventaSum)
	}
	// Los 3 métodos siguen clasificados para trazabilidad.
	methodTotals := map[string]float64{}
	for _, mt := range report.TotalsByMethod.Sales {
		methodTotals[mt.Method] = mt.Total
	}
	if methodTotals["efectivo"] != 100 || methodTotals["yape"] != 100 || methodTotals["transferencia"] != 100 {
		t.Errorf("TotalsByMethod.Sales incompleto: %+v", report.TotalsByMethod.Sales)
	}
}
