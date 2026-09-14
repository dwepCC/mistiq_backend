package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Test combinado de integración: venta en efectivo + venta no-efectivo + compra en efectivo +
// compra no-efectivo, todo en la MISMA sesión de caja. No existía ningún test que ejecutara los
// cuatro casos juntos. Reproduce exactamente el camino real de producción a nivel de servicio
// (sale_service.Create y purchase_service.Create llaman a estas mismas funciones de
// internal/cashbank/service — no se puede importar internal/purchases/service aquí sin crear un
// ciclo de imports, porque ese paquete a su vez importa este).
//
// Verifica: el balance final por método (GetSessionBalanceSummary, fuente única de verdad de
// Fase 1) y el efectivo esperado (getExpectedBalance, el mismo que usa CloseSession) son
// correctos; ningún movimiento se duplica; Yape/transferencia/tarjeta no inflan ni desinflan el
// efectivo físico.

func setupSessionSaleAndPurchaseTestDB(t *testing.T) *gorm.DB {
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

// createSaleForSession replica sale_service.Create: crea la venta + su línea de pago y llama a
// RecordPayment — una venta SIEMPRE queda con cash_session_id (cualquier método), igual que en
// producción.
func createSaleForSession(t *testing.T, db *gorm.DB, svc *CashBankService, saleID, sessionID uint, method string, amount float64) {
	t.Helper()
	sale := &database.TenantSale{
		ID: saleID, BranchID: 1, UserID: 1, CashSessionID: &sessionID,
		SeriesID: 1, DocType: "boleta", Series: "B001", Correlative: saleID,
		Number: fmt.Sprintf("B001-%d", saleID), IssueDate: time.Now(),
		Total: amount, Status: "paid", CreatedAt: time.Now(),
	}
	if err := db.Create(sale).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSalePayment{SaleID: saleID, Method: method, Amount: amount, CashSessionID: &sessionID, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordPayment(db, method, amount, &sessionID, sale.Number, "Venta "+sale.Number, &saleID, 1); err != nil {
		t.Fatal(err)
	}
}

// createPurchaseForSession replica purchase_service.Create: resuelve la sesión con
// ResolveCashSessionForPayments (igual que el servicio real) y llama a RecordExpensePayment. Una
// compra en efectivo exige sesión abierta (auto-resuelta si explicitSessionID es nil); una compra
// no-efectivo puede o no traer cash_session_id — aquí se pasa explícito para probar que, con la
// corrección de validación de sesión ya commiteada (895b153), una compra no-efectivo SÍ puede
// quedar trazada a la sesión indicada.
func createPurchaseForSession(t *testing.T, db *gorm.DB, svc *CashBankService, purchaseID, branchID, userID uint, method string, amount float64, explicitSessionID *uint) *uint {
	t.Helper()
	resolved, err := svc.ResolveCashSessionForPayments(branchID, userID, explicitSessionID,
		[]PaymentLineInput{{Method: method, Amount: amount}})
	if err != nil {
		t.Fatal(err)
	}
	purchase := &database.TenantPurchase{
		ID: purchaseID, BranchID: branchID, UserID: userID, DocType: "factura",
		Series: "F001", Number: fmt.Sprintf("%d", purchaseID), IssueDate: time.Now(),
		Total: amount, PaymentMethod: method, Status: "received", CashSessionID: resolved, CreatedAt: time.Now(),
	}
	if err := db.Create(purchase).Error; err != nil {
		t.Fatal(err)
	}
	docNumber := purchase.Series + "-" + purchase.Number
	if err := svc.RecordExpensePayment(db, method, amount, resolved, docNumber, "Compra "+docNumber, &purchaseID, userID); err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestSession_ventaEfectivo_ventaNoEfectivo_compraEfectivo_compraNoEfectivo(t *testing.T) {
	db := setupSessionSaleAndPurchaseTestDB(t)
	svc := NewCashBankService(db)

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

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 100, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}

	// Venta en efectivo: S/ 200.
	createSaleForSession(t, db, svc, 1, session.ID, "cash", 200)
	// Venta no-efectivo (Yape): S/ 150.
	createSaleForSession(t, db, svc, 2, session.ID, "yape", 150)
	// Compra en efectivo: S/ 80 (sesión auto-resuelta, como en producción).
	createPurchaseForSession(t, db, svc, 101, 1, 1, "cash", 80, nil)
	// Compra no-efectivo (transferencia): S/ 120, trazada explícitamente a esta sesión.
	purchaseCashSessionID := createPurchaseForSession(t, db, svc, 102, 1, 1, "transferencia", 120, &session.ID)

	if purchaseCashSessionID == nil || *purchaseCashSessionID != session.ID {
		t.Fatalf("compra no-efectivo debía quedar trazada a la sesión %d, got %v", session.ID, purchaseCashSessionID)
	}

	// --- Efectivo esperado (el mismo cálculo que usa CloseSession) ---
	// 100 apertura + 200 venta efectivo - 80 compra efectivo = 220. Yape/transferencia no tocan
	// esto en absoluto.
	wantExpected := 220.0
	if got := svc.getExpectedBalance(session.ID); got != wantExpected {
		t.Errorf("getExpectedBalance = %v, want %v", got, wantExpected)
	}

	// --- Balance por método (fuente única de verdad de la vista de Caja, Fase 1) ---
	summary, err := svc.GetSessionBalanceSummary(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	byMethod := map[string]MethodBalance{}
	for _, mb := range summary.ByMethod {
		byMethod[mb.Method] = mb
	}
	if got := byMethod["efectivo"].Net; got != wantExpected {
		t.Errorf("efectivo.Net = %v, want %v", got, wantExpected)
	}
	if !byMethod["efectivo"].IsCash {
		t.Error("efectivo debe marcarse IsCash=true")
	}
	if got := byMethod["yape"].Net; got != 150 {
		t.Errorf("yape.Net = %v, want 150 (solo la venta, sin egresos yape)", got)
	}
	if byMethod["yape"].IsCash {
		t.Error("yape NO debe marcarse IsCash")
	}
	if got := byMethod["transferencia"].Net; got != -120 {
		t.Errorf("transferencia.Net = %v, want -120 (la compra por transferencia)", got)
	}
	if byMethod["transferencia"].IsCash {
		t.Error("transferencia NO debe marcarse IsCash")
	}
	if summary.CashExpected != wantExpected {
		t.Errorf("CashExpected = %v, want %v (igual a getExpectedBalance)", summary.CashExpected, wantExpected)
	}
	// El efectivo esperado NUNCA debe coincidir con el total de todos los métodos cuando hay
	// medios no-efectivo de por medio — si coincide, algo está sumando de más.
	if summary.CashExpected == summary.Total {
		t.Error("CashExpected no debería igualar Total: yape/transferencia no deben inflar el efectivo físico")
	}

	// --- Reporte (GetSessionReport / CashPhysical, ya corregido en esta misma tarea) ---
	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.CashPhysical.PhysicalBalance != wantExpected {
		t.Errorf("CashPhysical.PhysicalBalance = %v, want %v", report.CashPhysical.PhysicalBalance, wantExpected)
	}
	if report.Totals.FinalBalance != wantExpected {
		t.Errorf("Totals.FinalBalance = %v, want %v", report.Totals.FinalBalance, wantExpected)
	}
	// Trazabilidad de VENTAS: efectivo y Yape se siguen clasificando, no desaparecen del reporte
	// (GetSessionReport lee las ventas de tenant_sale_payments, no de tenant_cash_movements).
	salesByMethod := map[string]float64{}
	for _, mt := range report.TotalsByMethod.Sales {
		salesByMethod[mt.Method] = mt.Total
	}
	if salesByMethod["efectivo"] != 200 || salesByMethod["yape"] != 150 {
		t.Errorf("TotalsByMethod.Sales incompleto: %+v", report.TotalsByMethod.Sales)
	}

	// Trazabilidad de COMPRAS: efectivo y transferencia se clasifican ambas (corrección de
	// listNonCashPurchasesForSession — antes la compra no-efectivo era invisible aquí porque
	// GetSessionReport solo leía tenant_cash_movements, donde una compra no-efectivo nunca
	// aparece).
	purchasesByMethod := map[string]float64{}
	for _, mt := range report.TotalsByMethod.Purchases {
		purchasesByMethod[mt.Method] = mt.Total
	}
	if purchasesByMethod["efectivo"] != 80 || purchasesByMethod["transferencia"] != 120 {
		t.Errorf("TotalsByMethod.Purchases incompleto: %+v", report.TotalsByMethod.Purchases)
	}

	// --- Sin doble conteo ---
	// Exactamente 2 filas de venta en IncomeDetail (una por venta, sin duplicar la de efectivo
	// por su TenantCashMovement asociado) y su suma debe ser 350, no más.
	ventaRows := 0
	var ventaSum float64
	for _, row := range report.IncomeDetail {
		if row.Type == "venta" {
			ventaRows++
			ventaSum += row.Amount
		}
	}
	if ventaRows != 2 {
		t.Errorf("IncomeDetail trae %d filas de venta, want 2", ventaRows)
	}
	if ventaSum != 350 {
		t.Errorf("suma de IncomeDetail venta = %v, want 350 (200+150, sin doble conteo)", ventaSum)
	}
	// Exactamente 2 filas de compra en ExpenseDetail (efectivo + transferencia), suma 200 — ninguna
	// se duplica ni se cuenta dos veces.
	compraRows := 0
	var compraSum float64
	for _, row := range report.ExpenseDetail {
		if row.Type == "compra" {
			compraRows++
			compraSum += row.Amount
		}
	}
	if compraRows != 2 {
		t.Errorf("ExpenseDetail trae %d filas de compra, want 2", compraRows)
	}
	if compraSum != 200 {
		t.Errorf("suma de ExpenseDetail compra = %v, want 200 (80+120, sin doble conteo)", compraSum)
	}

	// La compra no-efectivo NO debe afectar el efectivo físico: PhysicalBalance/FinalBalance ya se
	// verificaron arriba como wantExpected (220) — este total no incluye los 120 de la
	// transferencia, pese a que la compra ahora sí aparece en TotalsByMethod/ExpenseDetail.
}
