package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// Cierre/arqueo en soles: el efectivo de una venta en USD entra a la sesión con su equivalente (monto × TC del documento).
func TestSessionClose_UsesSolesEquivalentForUSDDocuments(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 100, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	rate := 4.0
	createReportSaleCur(t, db, svc, 1, session.ID, map[string]float64{"cash": 27}, "USD", &rate) // US$ 27 → S/ 108 en efectivo
	createReportSale(t, db, svc, 2, session.ID, map[string]float64{"cash": 50})                  // S/ 50 en efectivo
	createReportSaleCur(t, db, svc, 3, session.ID, map[string]float64{"yape": 10}, "USD", &rate) // US$ 10 → S/ 40 por Yape

	// Esperado del arqueo: 100 + 108 + 50 = 258 (antes: 100 + 27 + 50 = 177, mezclando dólares como soles).
	if got := svc.getExpectedBalance(session.ID); got != 258 {
		t.Fatalf("esperado = %v, quiere 258", got)
	}
	sum, err := svc.GetSessionBalanceSummary(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]MethodBalance{}
	for _, m := range sum.ByMethod {
		by[m.Method] = m
	}
	if by["efectivo"].Income != 158 || by["efectivo"].Net != 258 || by["yape"].Income != 40 || sum.Total != 298 || sum.CashExpected != 258 {
		t.Errorf("resumen en soles: efectivo=%+v yape=%+v total=%v esperado=%v", by["efectivo"], by["yape"], sum.Total, sum.CashExpected)
	}
	if !sum.HasForeign || sum.RateMissingCount != 0 {
		t.Errorf("debe avisar de documentos en dólares: %+v", sum)
	}
	rep, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := rep.Currency
	if !c.HasForeign || c.PhysicalBalancePEN != 258 || c.TotalIncomePEN != 198 || c.CashIncomePEN != 158 {
		t.Errorf("bloque de moneda: %+v", c)
	}
	by2 := map[string]MovementCurrencyTotal{}
	for _, x := range c.ByCurrency {
		by2[x.Currency] = x
	}
	if by2["USD"].SumIncome != 37 || by2["USD"].SumIncomePEN != 148 || by2["PEN"].SumIncome != 50 {
		t.Errorf("por moneda: %+v", c.ByCurrency)
	}

	// Cierre: lo contado en soles se compara con el esperado en soles.
	if err := svc.CloseSession(session.ID, 1, 258, "", nil, true); err != nil {
		t.Fatal(err)
	}
	var closed database.TenantCashSession
	db.First(&closed, session.ID)
	if closed.ExpectedBalance == nil || *closed.ExpectedBalance != 258 || closed.Difference == nil || *closed.Difference != 0 {
		t.Errorf("cierre: esperado=%v diferencia=%v", closed.ExpectedBalance, closed.Difference)
	}
}

// Una sesión solo en soles queda idéntica y sin el bloque de moneda extranjera.
func TestSessionClose_PENOnlySessionUnchanged(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 50, OpenedAt: time.Now()}
	db.Create(session)
	createReportSale(t, db, svc, 1, session.ID, map[string]float64{"cash": 200})
	if got := svc.getExpectedBalance(session.ID); got != 250 {
		t.Errorf("esperado = %v, quiere 250", got)
	}
	rep, _ := svc.GetSessionReport(session.ID)
	if rep.Currency.HasForeign {
		t.Errorf("sin dólares no debe haber bloque de moneda extranjera: %+v", rep.Currency)
	}
	if sum, _ := svc.GetSessionBalanceSummary(session.ID); sum.HasForeign {
		t.Error("el resumen no debe marcar moneda extranjera")
	}
}

// createReportSaleCur como createReportSale pero con la venta ya en la moneda indicada al registrar los pagos (así el snapshot
// de cada movimiento nace con su moneda y TC).
func createReportSaleCur(t *testing.T, db *gorm.DB, svc *CashBankService, saleID, sessionID uint, lines map[string]float64, currency string, rate *float64) {
	t.Helper()
	var total float64
	for _, amt := range lines {
		total += amt
	}
	sale := &database.TenantSale{
		ID: saleID, BranchID: 1, UserID: 1, CashSessionID: &sessionID, SeriesID: 1, DocType: "boleta", Series: "B001", Correlative: saleID,
		Number: fmt.Sprintf("B001-%d", saleID), IssueDate: time.Now(), Total: total, Status: "paid", CreatedAt: time.Now(),
		Currency: currency, ExchangeRate: rate,
	}
	if err := db.Create(sale).Error; err != nil {
		t.Fatal(err)
	}
	for method, amt := range lines {
		if err := db.Create(&database.TenantSalePayment{SaleID: saleID, Method: method, Amount: amt, CashSessionID: &sessionID, CreatedAt: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
		if err := svc.RecordPayment(db, method, amt, &sessionID, sale.Number, "Venta "+sale.Number, &saleID, 1); err != nil {
			t.Fatal(err)
		}
	}
}

// Snapshot (V182): el movimiento guarda su moneda, TC y equivalente al crearse; si luego se edita el TC del documento, el saldo
// de la sesión NO cambia.
func TestMovementSnapshot_StoredAtCreationAndStableAfterDocumentEdit(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 0, OpenedAt: time.Now()}
	db.Create(session)
	rate := 4.0
	createReportSaleCur(t, db, svc, 1, session.ID, map[string]float64{"cash": 25}, "USD", &rate)
	createReportSale(t, db, svc, 2, session.ID, map[string]float64{"cash": 40})

	var mv []database.TenantCashMovement
	db.Order("id").Find(&mv)
	if len(mv) != 2 || mv[0].Currency != "USD" || mv[0].ExchangeRate == nil || *mv[0].ExchangeRate != 4 || mv[0].AmountPEN == nil || *mv[0].AmountPEN != 100 {
		t.Fatalf("snapshot USD: %+v", mv)
	}
	if mv[1].Currency != "PEN" || mv[1].ExchangeRate != nil || mv[1].AmountPEN == nil || *mv[1].AmountPEN != 40 {
		t.Fatalf("snapshot PEN: %+v", mv[1])
	}
	// Si el documento cambia después de cobrado, el esperado de caja no se mueve (usa el snapshot, no el TC actual).
	db.Model(&database.TenantSale{}).Where("id = ?", 1).Update("exchange_rate", 9.9)
	if got := svc.getExpectedBalance(session.ID); got != 140 {
		t.Errorf("esperado = %v, quiere 140 (100 + 40) aunque el TC del documento cambió", got)
	}
}

// La cuenta bancaria (siempre en soles) sube por el equivalente en soles de un cobro en USD, y la anulación lo devuelve igual.
func TestBankAccountBalance_UsesSolesEquivalentForUSD(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	db.Create(session)
	rate := 4.0
	createReportSaleCur(t, db, svc, 1, session.ID, map[string]float64{"yape": 10}, "USD", &rate)
	var acc database.TenantBankAccount
	db.Where("name = ?", "Yape").First(&acc)
	if acc.Balance != 40 {
		t.Fatalf("saldo Yape = %v, quiere 40 (US$10 × 4)", acc.Balance)
	}
	var mv database.TenantBankMovement
	db.Where("bank_account_id = ?", acc.ID).First(&mv)
	if err := svc.CreateBankReversal(db, mv, "anulación", "ref", "Anulación venta", "", 1); err != nil {
		t.Fatal(err)
	}
	db.First(&acc, acc.ID)
	if acc.Balance != 0 {
		t.Fatalf("saldo tras la reversión = %v, quiere 0", acc.Balance)
	}
}

// Detalle de la sesión de caja: filas y totales en SOLES (un documento en USD se convierte con su TC) y con el original a la vista.
func TestSessionReport_USDDocumentsShownInSoles(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 0, OpenedAt: time.Now()}
	db.Create(session)
	rate := 4.0
	createReportSaleCur(t, db, svc, 1, session.ID, map[string]float64{"cash": 27}, "USD", &rate) // US$ 27 → S/ 108
	createReportSaleCur(t, db, svc, 2, session.ID, map[string]float64{"yape": 10}, "USD", &rate) // US$ 10 → S/ 40
	createReportSale(t, db, svc, 3, session.ID, map[string]float64{"cash": 50})                  // S/ 50

	rep, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Totals.TotalSales != 198 || rep.Totals.TotalIncome != 158 {
		t.Errorf("totales en soles: ventas=%v (quiere 198) ingreso efectivo=%v (quiere 158)", rep.Totals.TotalSales, rep.Totals.TotalIncome)
	}
	var usdRows int
	var sum float64
	for _, r := range rep.IncomeDetail {
		sum += r.Amount
		if r.Currency == "USD" {
			usdRows++
			if r.Amount != r.OriginalAmount*4 {
				t.Errorf("fila USD: %v soles vs %v US$ (TC 4)", r.Amount, r.OriginalAmount)
			}
		}
	}
	if usdRows != 2 || sum != 198 {
		t.Errorf("detalle: filas USD=%d suma=%v (quiere 2 y 198)", usdRows, sum)
	}
}
