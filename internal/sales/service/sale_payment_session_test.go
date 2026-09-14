package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Implementación P0 — separación documento/pago/movimiento: TenantSalePayment ahora tiene su
// propio cash_session_id (la Caja donde OCURRIÓ ese pago), independiente de
// TenantSale.CashSessionID (la Caja donde se REGISTRÓ el documento). Para una venta normal (no
// una cobranza posterior de crédito) ambas coinciden porque el pago ocurre en el mismo momento
// que la venta — estos tests confirman exactamente eso, y que una venta a crédito sin cobrar no
// genera ningún pago ni movimiento.

func setupSalePaymentSessionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantCompanyConfig{}, &database.TenantDocumentSeries{}, &database.TenantContact{},
		&database.TenantSale{}, &database.TenantSaleItem{}, &database.TenantSalePayment{},
		&database.TenantSaleCreditInstallment{},
		&database.TenantCashSession{}, &database.TenantCashMovement{},
		&database.TenantBankAccount{}, &database.TenantBankMovement{},
		&database.TenantPaymentMethod{}, &database.TenantBranch{},
		&database.TenantInventoryOperationType{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.TenantCompanyConfig{ID: 1, SunatEnabled: true, TaxRate: 18}).Error; err != nil {
		t.Fatal(err)
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
	tarjetaAcc := &database.TenantBankAccount{Name: "POS", Type: "bank", Active: true}
	if err := db.Create(tarjetaAcc).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "tarjeta", Name: "Tarjeta", BankAccountID: &tarjetaAcc.ID, Active: true, DestinationType: "bank_account"}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func openSessionForSalePaymentTest(t *testing.T, db *gorm.DB, sessionID uint) {
	t.Helper()
	if err := db.Create(&database.TenantCashSession{
		ID: sessionID, BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func baseSaleInput(seriesID uint, docType string, payments []PaymentInput, total float64) CreateSaleInput {
	return CreateSaleInput{
		BranchID:  1,
		UserID:    1,
		SeriesID:  seriesID,
		DocType:   docType,
		IssueDate: time.Now(),
		Currency:  "PEN",
		TaxConfig: tax.DefaultConfig(),
		Payments:  payments,
		Items: []SaleItemInput{{
			Description:        "Producto",
			Unit:               "NIU",
			Quantity:           1,
			UnitPrice:          total,
			IgvAffectationType: "10",
			PriceIncludesIgv:   true,
		}},
	}
}

// TEST 1 — Venta al contado en efectivo: venta, pago y movimiento de efectivo, los 3 con la
// misma sesión.
func TestSaleCreate_contadoEfectivo_todaLaCadenaEnLaMismaSesion(t *testing.T) {
	db := setupSalePaymentSessionTestDB(t)
	openSessionForSalePaymentTest(t, db, 25)
	svc := NewSaleService(db)
	seriesID := seedSeries(t, db, "Boleta", "03", "B001")

	sale, err := svc.Create(baseSaleInput(seriesID, "03", []PaymentInput{{Method: "cash", Amount: 1000}}, 1000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sale.CashSessionID == nil || *sale.CashSessionID != 25 {
		t.Fatalf("sale.CashSessionID = %v, want 25", sale.CashSessionID)
	}

	var payment database.TenantSalePayment
	if err := db.Where("sale_id = ?", sale.ID).First(&payment).Error; err != nil {
		t.Fatal(err)
	}
	if payment.CashSessionID == nil || *payment.CashSessionID != 25 {
		t.Fatalf("payment.CashSessionID = %v, want 25", payment.CashSessionID)
	}

	var cashMov database.TenantCashMovement
	if err := db.Where("sale_id = ?", sale.ID).First(&cashMov).Error; err != nil {
		t.Fatal(err)
	}
	if cashMov.CashSessionID != 25 {
		t.Fatalf("cash_movement.CashSessionID = %v, want 25", cashMov.CashSessionID)
	}
	var bankCount int64
	db.Model(&database.TenantBankMovement{}).Where("sale_id = ?", sale.ID).Count(&bankCount)
	if bankCount != 0 {
		t.Errorf("no debe generarse ningún movimiento bancario para un pago 100%% efectivo, got %d", bankCount)
	}
}

// TEST 2 — Venta al contado por Yape: venta, pago y movimiento bancario en la misma sesión, sin
// ningún movimiento de efectivo.
func TestSaleCreate_contadoYape_todaLaCadenaEnLaMismaSesionSinEfectivo(t *testing.T) {
	db := setupSalePaymentSessionTestDB(t)
	openSessionForSalePaymentTest(t, db, 25)
	svc := NewSaleService(db)
	seriesID := seedSeries(t, db, "Boleta", "03", "B001")

	sale, err := svc.Create(baseSaleInput(seriesID, "03", []PaymentInput{{Method: "yape", Amount: 1000}}, 1000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sale.CashSessionID == nil || *sale.CashSessionID != 25 {
		t.Fatalf("sale.CashSessionID = %v, want 25", sale.CashSessionID)
	}

	var payment database.TenantSalePayment
	if err := db.Where("sale_id = ?", sale.ID).First(&payment).Error; err != nil {
		t.Fatal(err)
	}
	if payment.CashSessionID == nil || *payment.CashSessionID != 25 {
		t.Fatalf("payment.CashSessionID = %v, want 25", payment.CashSessionID)
	}

	var bankMov database.TenantBankMovement
	if err := db.Where("sale_id = ?", sale.ID).First(&bankMov).Error; err != nil {
		t.Fatal(err)
	}
	if bankMov.CashSessionID == nil || *bankMov.CashSessionID != 25 {
		t.Fatalf("bank_movement.CashSessionID = %v, want 25", bankMov.CashSessionID)
	}
	var cashCount int64
	db.Model(&database.TenantCashMovement{}).Where("sale_id = ?", sale.ID).Count(&cashCount)
	if cashCount != 0 {
		t.Errorf("no debe generarse ningún movimiento de efectivo para un pago 100%% Yape, got %d", cashCount)
	}
}

// TEST 3 — Venta mixta: efectivo + Yape + tarjeta. 3 líneas de pago, 1 cash movement, 2 bank
// movements, todos en la misma sesión, totales correctos.
func TestSaleCreate_ventaMixta_tresLineasMismaSesion(t *testing.T) {
	db := setupSalePaymentSessionTestDB(t)
	openSessionForSalePaymentTest(t, db, 25)
	svc := NewSaleService(db)
	seriesID := seedSeries(t, db, "Boleta", "03", "B001")

	sale, err := svc.Create(baseSaleInput(seriesID, "03", []PaymentInput{
		{Method: "cash", Amount: 400},
		{Method: "yape", Amount: 300},
		{Method: "tarjeta", Amount: 300},
	}, 1000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var payments []database.TenantSalePayment
	db.Where("sale_id = ?", sale.ID).Find(&payments)
	if len(payments) != 3 {
		t.Fatalf("TenantSalePayment: got %d, want 3", len(payments))
	}
	for _, p := range payments {
		if p.CashSessionID == nil || *p.CashSessionID != 25 {
			t.Errorf("payment %s.CashSessionID = %v, want 25", p.Method, p.CashSessionID)
		}
	}

	var cashMovs []database.TenantCashMovement
	db.Where("sale_id = ?", sale.ID).Find(&cashMovs)
	if len(cashMovs) != 1 {
		t.Fatalf("TenantCashMovement: got %d, want 1", len(cashMovs))
	}
	if cashMovs[0].Amount != 400 || cashMovs[0].CashSessionID != 25 {
		t.Errorf("cash movement inesperado: %+v", cashMovs[0])
	}

	var bankMovs []database.TenantBankMovement
	db.Where("sale_id = ?", sale.ID).Find(&bankMovs)
	if len(bankMovs) != 2 {
		t.Fatalf("TenantBankMovement: got %d, want 2", len(bankMovs))
	}
	var bankTotal float64
	for _, m := range bankMovs {
		bankTotal += m.Amount
		if m.CashSessionID == nil || *m.CashSessionID != 25 {
			t.Errorf("bank movement %+v sin sesión 25", m)
		}
	}
	if bankTotal != 600 {
		t.Errorf("total bank movements = %v, want 600 (300+300)", bankTotal)
	}
}

// TEST 4 — Venta a crédito sin adelanto: la venta conserva su sesión, pero no debe existir
// ningún pago, ningún movimiento de efectivo, ningún movimiento bancario, y no debe clasificarse
// como electrónico en el reporte (CxC = total).
func TestSaleCreate_creditoSinAdelanto_ceroMovimientos(t *testing.T) {
	db := setupSalePaymentSessionTestDB(t)
	openSessionForSalePaymentTest(t, db, 25)
	svc := NewSaleService(db)
	seriesID := seedSeries(t, db, "Boleta", "03", "B001")
	due := time.Now().Add(30 * 24 * time.Hour)

	input := baseSaleInput(seriesID, "03", nil, 3000)
	input.PaymentConditionCode = "credit"
	input.DueDate = &due

	sale, err := svc.Create(input)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sale.CashSessionID == nil || *sale.CashSessionID != 25 {
		t.Fatalf("sale.CashSessionID = %v, want 25", sale.CashSessionID)
	}
	if sale.Status != "credit" {
		t.Errorf("sale.Status = %q, want credit", sale.Status)
	}

	var paymentCount int64
	db.Model(&database.TenantSalePayment{}).Where("sale_id = ?", sale.ID).Count(&paymentCount)
	if paymentCount != 0 {
		t.Errorf("no debe existir ningún TenantSalePayment para una venta a crédito sin adelanto, got %d", paymentCount)
	}
	var cashCount, bankCount int64
	db.Model(&database.TenantCashMovement{}).Where("sale_id = ?", sale.ID).Count(&cashCount)
	db.Model(&database.TenantBankMovement{}).Where("sale_id = ?", sale.ID).Count(&bankCount)
	if cashCount != 0 || bankCount != 0 {
		t.Errorf("no debe existir ningún movimiento financiero: cash=%d bank=%d", cashCount, bankCount)
	}

	var installments []database.TenantSaleCreditInstallment
	db.Where("sale_id = ?", sale.ID).Find(&installments)
	var creditTotal float64
	for _, i := range installments {
		creditTotal += i.Amount
	}
	if creditTotal != 3000 {
		t.Errorf("CxC (suma de cuotas) = %v, want 3000", creditTotal)
	}
}

// TEST 5 — Venta a crédito CON adelanto: el adelanto sí genera pago + movimiento en la sesión de
// registro; el resto queda como CxC. No debe considerarse que ingresaron los 3,000 completos.
func TestSaleCreate_creditoConAdelanto_soloElAdelantoEsDineroRecibido(t *testing.T) {
	db := setupSalePaymentSessionTestDB(t)
	openSessionForSalePaymentTest(t, db, 25)
	svc := NewSaleService(db)
	seriesID := seedSeries(t, db, "Boleta", "03", "B001")
	due := time.Now().Add(30 * 24 * time.Hour)

	input := baseSaleInput(seriesID, "03", []PaymentInput{{Method: "cash", Amount: 500}}, 3000)
	input.PaymentConditionCode = "credit"
	input.DueDate = &due

	sale, err := svc.Create(input)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sale.CashSessionID == nil || *sale.CashSessionID != 25 {
		t.Fatalf("sale.CashSessionID = %v, want 25", sale.CashSessionID)
	}

	var payment database.TenantSalePayment
	if err := db.Where("sale_id = ?", sale.ID).First(&payment).Error; err != nil {
		t.Fatal(err)
	}
	if payment.Amount != 500 || payment.CashSessionID == nil || *payment.CashSessionID != 25 {
		t.Fatalf("payment inesperado: %+v", payment)
	}

	var cashMov database.TenantCashMovement
	if err := db.Where("sale_id = ?", sale.ID).First(&cashMov).Error; err != nil {
		t.Fatal(err)
	}
	if cashMov.Amount != 500 || cashMov.CashSessionID != 25 {
		t.Fatalf("cash movement inesperado: %+v", cashMov)
	}

	var installments []database.TenantSaleCreditInstallment
	db.Where("sale_id = ?", sale.ID).Find(&installments)
	var creditTotal float64
	for _, i := range installments {
		creditTotal += i.Amount
	}
	if creditTotal != 2500 {
		t.Errorf("CxC pendiente (suma de cuotas) = %v, want 2500 (3000-500)", creditTotal)
	}
}
