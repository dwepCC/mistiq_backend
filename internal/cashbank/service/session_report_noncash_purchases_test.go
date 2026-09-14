package service

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Corrección de la brecha "compras no-efectivo invisibles en GetSessionReport": una compra pagada
// por Yape/Plin/transferencia/tarjeta nunca crea un TenantCashMovement (recordDirectedPayment,
// rama bank_account, solo crea un TenantBankMovement) y, a diferencia de una venta, tampoco queda
// con cash_session_id (purchase_service.Create solo lo resuelve/exige para el camino en efectivo)
// — por eso GetSessionReport, que armaba el detalle de compras leyendo únicamente
// tenant_cash_movements, nunca las veía. listNonCashPurchasesForSession corrige esto leyendo
// tenant_purchases directamente (vínculo por cash_session_id si existe, o por sucursal+ventana de
// fecha si no — el caso real de hoy), sin tocar tenant_bank_movements y sin duplicar nada.

func setupSessionReportNonCashPurchasesTestDB(t *testing.T) *gorm.DB {
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

func seedNonCashPurchaseMethods(t *testing.T, db *gorm.DB) {
	t.Helper()
	accounts := map[string]*database.TenantBankAccount{
		"yape":          {Name: "Yape", Type: "wallet", Active: true},
		"plin":          {Name: "Plin", Type: "wallet", Active: true},
		"transferencia": {Name: "BCP", Type: "bank", Active: true},
		"tarjeta":       {Name: "POS", Type: "bank", Active: true},
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true, DestinationType: "cash"}).Error; err != nil {
		t.Fatal(err)
	}
	for code, acc := range accounts {
		if err := db.Create(acc).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.TenantPaymentMethod{Code: code, Name: code, BankAccountID: &acc.ID, Active: true, DestinationType: "bank_account"}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// createCashPurchase compra en efectivo: exige y queda con cash_session_id directo (el camino que
// ya funcionaba antes de esta corrección — debe seguir exactamente igual).
func createCashPurchase(t *testing.T, db *gorm.DB, svc *CashBankService, purchaseID, sessionID, branchID, userID uint, amount float64) {
	t.Helper()
	purchase := &database.TenantPurchase{
		ID: purchaseID, BranchID: branchID, UserID: userID, DocType: "factura",
		Series: "F001", Number: fmt.Sprintf("%d", purchaseID), IssueDate: time.Now(),
		Total: amount, PaymentMethod: "cash", Status: "received", CashSessionID: &sessionID, CreatedAt: time.Now(),
	}
	if err := db.Create(purchase).Error; err != nil {
		t.Fatal(err)
	}
	docNumber := purchase.Series + "-" + purchase.Number
	if err := svc.RecordExpensePayment(db, "cash", amount, &sessionID, docNumber, "Compra "+docNumber, &purchaseID, userID); err != nil {
		t.Fatal(err)
	}
}

// createNonCashPurchase reproduce EXACTAMENTE el camino real de producción hoy: purchase_service
// .Create pasa cash_session_id=nil a ResolveCashSessionForPayments para cualquier método que no
// sea efectivo, así que la compra queda con cash_session_id NULL (ver comentario del campo en
// migrations.go). GetSessionReport debe encontrarla igual, por sucursal + ventana de fecha.
func createNonCashPurchase(t *testing.T, db *gorm.DB, svc *CashBankService, purchaseID, branchID, userID uint, method string, amount float64) {
	t.Helper()
	purchase := &database.TenantPurchase{
		ID: purchaseID, BranchID: branchID, UserID: userID, DocType: "factura",
		Series: "F001", Number: fmt.Sprintf("%d", purchaseID), IssueDate: time.Now(),
		Total: amount, PaymentMethod: method, Status: "received", CashSessionID: nil, CreatedAt: time.Now(),
	}
	if err := db.Create(purchase).Error; err != nil {
		t.Fatal(err)
	}
	docNumber := purchase.Series + "-" + purchase.Number
	if err := svc.RecordExpensePayment(db, method, amount, nil, docNumber, "Compra "+docNumber, &purchaseID, userID); err != nil {
		t.Fatal(err)
	}
}

func createSaleWithMethod(t *testing.T, db *gorm.DB, svc *CashBankService, saleID, sessionID, branchID, userID uint, method string, amount float64) {
	t.Helper()
	sale := &database.TenantSale{
		ID: saleID, BranchID: branchID, UserID: userID, CashSessionID: &sessionID,
		SeriesID: 1, DocType: "boleta", Series: "B001", Correlative: saleID,
		Number: fmt.Sprintf("B001-%d", saleID), IssueDate: time.Now(),
		Total: amount, Status: "paid", CreatedAt: time.Now(),
	}
	if err := db.Create(sale).Error; err != nil {
		t.Fatal(err)
	}
	// CashSessionID: sale_service.Create() SIEMPRE la pobla en cada TenantSalePayment (P0) — sin
	// esto, el fixture no reproduce el comportamiento real y GetSessionReport (que ahora filtra
	// "dinero recibido" por TenantSalePayment.CashSessionID) no vería el pago.
	if err := db.Create(&database.TenantSalePayment{SaleID: saleID, Method: method, Amount: amount, CashSessionID: &sessionID, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordPayment(db, method, amount, &sessionID, sale.Number, "Venta "+sale.Number, &saleID, userID); err != nil {
		t.Fatal(err)
	}
}

// 1. Compra en efectivo: aparece en Purchases, con método efectivo, y SÍ afecta el efectivo
// físico (esto no cambia con esta corrección — es el camino que ya funcionaba).
func TestGetSessionReport_compraEfectivo_apareceYAfectaFisico(t *testing.T) {
	db := setupSessionReportNonCashPurchasesTestDB(t)
	svc := NewCashBankService(db)
	seedNonCashPurchaseMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 200, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createCashPurchase(t, db, svc, 201, session.ID, 1, 1, 100)

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	purchasesByMethod := map[string]float64{}
	for _, mt := range report.TotalsByMethod.Purchases {
		purchasesByMethod[mt.Method] = mt.Total
	}
	if purchasesByMethod["efectivo"] != 100 {
		t.Errorf("TotalsByMethod.Purchases[efectivo] = %v, want 100", purchasesByMethod["efectivo"])
	}
	found := false
	for _, row := range report.ExpenseDetail {
		if row.Type == "compra" && row.PaymentMethod == "efectivo" && row.Amount == 100 {
			found = true
		}
	}
	if !found {
		t.Errorf("ExpenseDetail no trae la compra en efectivo: %+v", report.ExpenseDetail)
	}
	wantPhysical := 100.0 // 200 apertura - 100 compra en efectivo
	if report.CashPhysical.PhysicalBalance != wantPhysical {
		t.Errorf("PhysicalBalance = %v, want %v (la compra en efectivo SÍ debe afectar el físico)", report.CashPhysical.PhysicalBalance, wantPhysical)
	}
	if got := svc.getExpectedBalance(session.ID); got != wantPhysical {
		t.Errorf("getExpectedBalance = %v, want %v", got, wantPhysical)
	}
}

// 2. Compra por Yape/Plin: aparece en Purchases con su método correcto, y NO afecta
// PhysicalBalance (el dinero salió de la billetera digital, no del cajón).
func TestGetSessionReport_compraYapeYPlin_aparecenSinAfectarFisico(t *testing.T) {
	db := setupSessionReportNonCashPurchasesTestDB(t)
	svc := NewCashBankService(db)
	seedNonCashPurchaseMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 500, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createNonCashPurchase(t, db, svc, 301, 1, 1, "yape", 90)
	createNonCashPurchase(t, db, svc, 302, 1, 1, "plin", 40)

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	purchasesByMethod := map[string]float64{}
	for _, mt := range report.TotalsByMethod.Purchases {
		purchasesByMethod[mt.Method] = mt.Total
	}
	if purchasesByMethod["yape"] != 90 {
		t.Errorf("TotalsByMethod.Purchases[yape] = %v, want 90", purchasesByMethod["yape"])
	}
	if purchasesByMethod["plin"] != 40 {
		t.Errorf("TotalsByMethod.Purchases[plin] = %v, want 40", purchasesByMethod["plin"])
	}
	if report.CashPhysical.PhysicalBalance != 500 {
		t.Errorf("PhysicalBalance = %v, want 500 (yape/plin no deben tocar el físico)", report.CashPhysical.PhysicalBalance)
	}
	if report.Totals.FinalBalance != 500 {
		t.Errorf("Totals.FinalBalance = %v, want 500", report.Totals.FinalBalance)
	}
}

// 3. Compra por transferencia: aparece en Purchases con "transferencia", y NO afecta
// PhysicalBalance.
func TestGetSessionReport_compraTransferencia_apareceSinAfectarFisico(t *testing.T) {
	db := setupSessionReportNonCashPurchasesTestDB(t)
	svc := NewCashBankService(db)
	seedNonCashPurchaseMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 300, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createNonCashPurchase(t, db, svc, 401, 1, 1, "transferencia", 175)

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	purchasesByMethod := map[string]float64{}
	for _, mt := range report.TotalsByMethod.Purchases {
		purchasesByMethod[mt.Method] = mt.Total
	}
	if purchasesByMethod["transferencia"] != 175 {
		t.Errorf("TotalsByMethod.Purchases[transferencia] = %v, want 175", purchasesByMethod["transferencia"])
	}
	if report.CashPhysical.PhysicalBalance != 300 {
		t.Errorf("PhysicalBalance = %v, want 300 (la transferencia no debe tocar el físico)", report.CashPhysical.PhysicalBalance)
	}
}

// 4. Venta no-efectivo + compra no-efectivo en la misma sesión: ambas aparecen, sin doble
// conteo, y los totales por método son correctos.
func TestGetSessionReport_ventaYCompraNoEfectivo_sinDobleConteo(t *testing.T) {
	db := setupSessionReportNonCashPurchasesTestDB(t)
	svc := NewCashBankService(db)
	seedNonCashPurchaseMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createSaleWithMethod(t, db, svc, 501, session.ID, 1, 1, "yape", 200)
	createNonCashPurchase(t, db, svc, 502, 1, 1, "plin", 50)

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	salesByMethod := map[string]float64{}
	for _, mt := range report.TotalsByMethod.Sales {
		salesByMethod[mt.Method] = mt.Total
	}
	purchasesByMethod := map[string]float64{}
	for _, mt := range report.TotalsByMethod.Purchases {
		purchasesByMethod[mt.Method] = mt.Total
	}
	if salesByMethod["yape"] != 200 {
		t.Errorf("TotalsByMethod.Sales[yape] = %v, want 200", salesByMethod["yape"])
	}
	if purchasesByMethod["plin"] != 50 {
		t.Errorf("TotalsByMethod.Purchases[plin] = %v, want 50", purchasesByMethod["plin"])
	}
	// Sin doble conteo: exactamente 1 fila de venta y 1 de compra.
	ventaRows, compraRows := 0, 0
	for _, row := range report.IncomeDetail {
		if row.Type == "venta" {
			ventaRows++
		}
	}
	for _, row := range report.ExpenseDetail {
		if row.Type == "compra" {
			compraRows++
		}
	}
	if ventaRows != 1 {
		t.Errorf("IncomeDetail trae %d filas de venta, want 1", ventaRows)
	}
	if compraRows != 1 {
		t.Errorf("ExpenseDetail trae %d filas de compra, want 1", compraRows)
	}
	// Ninguna de las dos toca el efectivo físico (sesión sin apertura ni movimientos en efectivo).
	if report.CashPhysical.PhysicalBalance != 0 {
		t.Errorf("PhysicalBalance = %v, want 0", report.CashPhysical.PhysicalBalance)
	}
}

// 5. Sesión completa: venta efectivo + venta Yape + compra efectivo + compra transferencia. El
// efectivo esperado debe ser únicamente efectivo, los medios digitales deben quedar clasificados,
// y compras/ventas deben aparecer completas (nada invisible).
func TestGetSessionReport_sesionCompleta_efectivoEsperadoSoloEfectivo(t *testing.T) {
	db := setupSessionReportNonCashPurchasesTestDB(t)
	svc := NewCashBankService(db)
	seedNonCashPurchaseMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createSaleWithMethod(t, db, svc, 601, session.ID, 1, 1, "cash", 300)
	createSaleWithMethod(t, db, svc, 602, session.ID, 1, 1, "yape", 150)
	createCashPurchase(t, db, svc, 603, session.ID, 1, 1, 80)
	createNonCashPurchase(t, db, svc, 604, 1, 1, "transferencia", 60)

	wantExpected := 220.0 // 300 venta efectivo - 80 compra efectivo; yape/transferencia no cuentan
	if got := svc.getExpectedBalance(session.ID); got != wantExpected {
		t.Fatalf("getExpectedBalance = %v, want %v (fixture inconsistente)", got, wantExpected)
	}

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.CashPhysical.PhysicalBalance != wantExpected {
		t.Errorf("PhysicalBalance = %v, want %v", report.CashPhysical.PhysicalBalance, wantExpected)
	}
	if report.Totals.FinalBalance != wantExpected {
		t.Errorf("Totals.FinalBalance = %v, want %v", report.Totals.FinalBalance, wantExpected)
	}

	salesByMethod := map[string]float64{}
	for _, mt := range report.TotalsByMethod.Sales {
		salesByMethod[mt.Method] = mt.Total
	}
	purchasesByMethod := map[string]float64{}
	for _, mt := range report.TotalsByMethod.Purchases {
		purchasesByMethod[mt.Method] = mt.Total
	}
	if salesByMethod["efectivo"] != 300 || salesByMethod["yape"] != 150 {
		t.Errorf("TotalsByMethod.Sales incompleto: %+v", report.TotalsByMethod.Sales)
	}
	if purchasesByMethod["efectivo"] != 80 || purchasesByMethod["transferencia"] != 60 {
		t.Errorf("TotalsByMethod.Purchases incompleto: %+v", report.TotalsByMethod.Purchases)
	}
	if report.Totals.TotalPurchases != 140 { // 80 + 60, todas las compras, cualquier método
		t.Errorf("Totals.TotalPurchases = %v, want 140", report.Totals.TotalPurchases)
	}
	if report.Totals.TotalSalesCommercial != 450 { // 300 + 150
		t.Errorf("Totals.TotalSalesCommercial = %v, want 450", report.Totals.TotalSalesCommercial)
	}

	ventaRows, compraRows := 0, 0
	for _, row := range report.IncomeDetail {
		if row.Type == "venta" {
			ventaRows++
		}
	}
	for _, row := range report.ExpenseDetail {
		if row.Type == "compra" {
			compraRows++
		}
	}
	if ventaRows != 2 {
		t.Errorf("IncomeDetail trae %d filas de venta, want 2 (ninguna compra/venta debe faltar)", ventaRows)
	}
	if compraRows != 2 {
		t.Errorf("ExpenseDetail trae %d filas de compra, want 2 (ninguna compra/venta debe faltar)", compraRows)
	}
}

// 6. El contrato JSON que consumen CashReportsPage.tsx / cashReportExcel.ts / el PDF de sesión no
// cambió de forma: las claves que esos archivos leen (totals_by_method.purchases,
// expense_detail, cash_physical.physical_balance, totals.final_balance) siguen presentes con el
// mismo nombre y forma — esta corrección solo llena datos que antes faltaban, no cambia el
// contrato.
func TestGetSessionReport_contratoJSONSinCambios(t *testing.T) {
	db := setupSessionReportNonCashPurchasesTestDB(t)
	svc := NewCashBankService(db)
	seedNonCashPurchaseMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 10, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createNonCashPurchase(t, db, svc, 701, 1, 1, "tarjeta", 25)

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"totals", "totals_by_method", "expense_detail", "income_detail", "cash_physical", "electronic"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("el JSON del reporte perdió la clave %q — cambio de contrato no autorizado", key)
		}
	}
	totalsByMethod, _ := generic["totals_by_method"].(map[string]interface{})
	if totalsByMethod == nil {
		t.Fatal("totals_by_method no es un objeto")
	}
	if _, ok := totalsByMethod["purchases"]; !ok {
		t.Error("totals_by_method.purchases desapareció del JSON")
	}
	cashPhysical, _ := generic["cash_physical"].(map[string]interface{})
	if cashPhysical == nil {
		t.Fatal("cash_physical no es un objeto")
	}
	if _, ok := cashPhysical["physical_balance"]; !ok {
		t.Error("cash_physical.physical_balance desapareció del JSON")
	}
}
