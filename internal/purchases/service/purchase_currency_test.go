package service

import (
	"strings"
	"testing"
	"time"

	posvc "tukifac/internal/purchaseorders/service"
	"tukifac/pkg/dailyfx"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"
)

// Compras en USD (Fase 2): el documento conserva su moneda y TC; el costo del inventario y el precio de
// compra del catálogo se llevan en soles (costo × TC).

func stockProduct(t *testing.T, svc *PurchaseService, code string) uint {
	t.Helper()
	p := &database.TenantProduct{Code: code, Name: code, Type: "product", Unit: "NIU", SalePrice: 100, PurchasePrice: 1,
		TaxRate: 18, IgvAffectationType: "10", ManageStock: true, Active: true}
	if err := svc.db.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func usdInput(pid uint, number string, rate *float64) CreatePurchaseInput {
	return CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: number, IssueDate: time.Now(),
		Currency: "USD", ExchangeRate: rate,
		Items:     []PurchaseItemInput{{ProductID: &pid, Description: "Equipo", Quantity: 2, UnitCost: 10, IgvAffectationType: "10"}},
		TaxConfig: tax.Config{TaxRate: 18},
	}
}

func TestPurchaseUSD_RequiresExchangeRateAndValidCurrency(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	newOpenCashSession(t, db, 1, 1)
	pid := stockProduct(t, svc, "P-USD0")

	if _, err := svc.Create(usdInput(pid, "1", nil)); err == nil || !strings.Contains(err.Error(), "tipo de cambio") {
		t.Errorf("USD sin TC debe rechazarse, err=%v", err)
	}
	zero := 0.0
	if _, err := svc.Create(usdInput(pid, "2", &zero)); err == nil {
		t.Error("USD con TC 0 debe rechazarse")
	}
	in := usdInput(pid, "3", nil)
	in.Currency = "EUR"
	if _, err := svc.Create(in); err == nil {
		t.Error("EUR no soportada debe rechazarse")
	}
	var n int64
	db.Model(&database.TenantPurchase{}).Count(&n)
	if n != 0 {
		t.Errorf("ninguna compra rechazada debe persistir, hay %d", n)
	}
}

func TestPurchaseUSD_StockCostAndCatalogPriceInSoles(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	newOpenCashSession(t, db, 1, 1)
	pid := stockProduct(t, svc, "P-USD1")
	rate := 3.75

	p, err := svc.Create(usdInput(pid, "10", &rate))
	if err != nil {
		t.Fatal(err)
	}
	// El documento queda en dólares con su TC; 2 × 10 = 20 + IGV 3.60 = 23.60.
	if p.Currency != "USD" || p.ExchangeRate == nil || *p.ExchangeRate != 3.75 {
		t.Errorf("moneda/TC: %+v %v", p.Currency, p.ExchangeRate)
	}
	if p.Total != 23.6 {
		t.Errorf("total en USD = %v, quiere 23.60", p.Total)
	}
	// Kardex: costo unitario en soles (10 × 3.75 = 37.50).
	var mov database.TenantStockMovement
	if err := db.Where("product_id = ? AND type = ?", pid, "in").First(&mov).Error; err != nil {
		t.Fatal(err)
	}
	if mov.UnitCost != 37.5 {
		t.Errorf("costo del kardex = %v, quiere 37.50 (soles)", mov.UnitCost)
	}
	// Precio de compra del catálogo también en soles.
	var prod database.TenantProduct
	db.First(&prod, pid)
	if prod.PurchasePrice != 37.5 {
		t.Errorf("purchase_price = %v, quiere 37.50", prod.PurchasePrice)
	}
	// El ítem conserva el costo del documento (USD).
	var it database.TenantPurchaseItem
	db.Where("purchase_id = ?", p.ID).First(&it)
	if it.UnitCost != 10 {
		t.Errorf("costo del ítem = %v, quiere 10 (moneda del documento)", it.UnitCost)
	}
}

func TestPurchasePEN_UnchangedAndIgnoresRate(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	newOpenCashSession(t, db, 1, 1)
	pid := stockProduct(t, svc, "P-PEN")
	rate := 3.75
	in := usdInput(pid, "20", &rate)
	in.Currency = "" // sin moneda = soles
	p, err := svc.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Currency != "PEN" || p.ExchangeRate != nil {
		t.Errorf("PEN no guarda TC: %s %v", p.Currency, p.ExchangeRate)
	}
	var mov database.TenantStockMovement
	db.Where("product_id = ? AND type = ?", pid, "in").First(&mov)
	if mov.UnitCost != 10 {
		t.Errorf("costo en soles sin conversión = %v, quiere 10", mov.UnitCost)
	}
}

func TestPurchaseUSD_VoidRestoresStockWithSolesCost(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	newOpenCashSession(t, db, 1, 1)
	pid := stockProduct(t, svc, "P-USD2")
	rate := 3.5
	p, err := svc.Create(usdInput(pid, "30", &rate))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Void(p.ID, 1); err != nil {
		t.Fatal(err)
	}
	var out database.TenantStockMovement
	if err := db.Where("product_id = ? AND type = ?", pid, "out").First(&out).Error; err != nil {
		t.Fatal(err)
	}
	if out.UnitCost != 35 || out.Quantity != 2 {
		t.Errorf("la anulación revierte con el costo en soles: qty=%v cost=%v", out.Quantity, out.UnitCost)
	}
}

func TestPurchaseFromOrder_USDMustMatchOrderCurrency(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	if err := db.AutoMigrate(&database.TenantPurchaseOrder{}, &database.TenantPurchaseOrderItem{}); err != nil {
		t.Fatal(err)
	}
	svc := NewPurchaseService(db)
	newOpenCashSession(t, db, 1, 1)
	pid := stockProduct(t, svc, "P-OCUSD")
	rate := 3.7
	order := &database.TenantPurchaseOrder{BranchID: 1, UserID: 1, SeriesID: 1, Series: "OC01", Correlative: 1, Number: "OC01-00000001",
		IssueDate: time.Now(), Currency: "USD", ExchangeRate: &rate, Total: 23.6, Status: posvc.StatusSent}
	db.Create(order)

	// Compra en soles sobre una orden en dólares: rechazada, y no deja rastro.
	pen := usdInput(pid, "40", nil)
	pen.Currency, pen.PurchaseOrderID = "PEN", &order.ID
	if _, err := svc.Create(pen); err == nil || !strings.Contains(err.Error(), "moneda de la orden") {
		t.Fatalf("PEN sobre orden USD debe rechazarse, err=%v", err)
	}
	var n int64
	db.Model(&database.TenantPurchase{}).Count(&n)
	if n != 0 {
		t.Errorf("la compra rechazada no debe persistir, hay %d", n)
	}
	// En dólares (con el TC de la fecha del comprobante, distinto al de la orden): pasa.
	invoiceRate := 3.72
	usd := usdInput(pid, "41", &invoiceRate)
	usd.PurchaseOrderID = &order.ID
	p, err := svc.Create(usd)
	if err != nil {
		t.Fatal(err)
	}
	var got database.TenantPurchaseOrder
	db.First(&got, order.ID)
	if got.Status != posvc.StatusReceived || got.PurchaseID == nil || *got.PurchaseID != p.ID {
		t.Errorf("la orden debe quedar recibida: %+v", got)
	}
}

// Reporte de compras: totales por moneda sin mezclar dólares con soles; las anuladas no cuentan.
func TestPurchaseTotalsByCurrency_SeparatesCurrenciesAndSkipsCancelled(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	newOpenCashSession(t, db, 1, 1)
	pid := stockProduct(t, svc, "P-TOT")
	rate := 3.5

	pen := usdInput(pid, "1", nil)
	pen.Currency = "PEN" // 2 × 10 → 20 + IGV 3.60 = 23.60 soles
	if _, err := svc.Create(pen); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(usdInput(pid, "2", &rate)); err != nil { // 23.60 USD → S/ 82.60
		t.Fatal(err)
	}
	cancelled, err := svc.Create(usdInput(pid, "3", &rate))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Void(cancelled.ID, 1); err != nil {
		t.Fatal(err)
	}

	totals, err := svc.TotalsByCurrency(PurchaseListParams{BranchID: 1})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]CurrencyTotal{}
	for _, tt := range totals {
		by[tt.Currency] = tt
	}
	if len(by) != 2 {
		t.Fatalf("debe haber una fila por moneda: %+v", totals)
	}
	if p := by["PEN"]; p.Count != 1 || p.Total != 23.6 || p.TotalPEN != 23.6 {
		t.Errorf("PEN: %+v", p)
	}
	if u := by["USD"]; u.Count != 1 || u.Total != 23.6 || u.TotalPEN != 82.6 { // 23.60 × 3.5
		t.Errorf("USD (sin la anulada): %+v", u)
	}
	// Otra sucursal no ve nada.
	if other, _ := svc.TotalsByCurrency(PurchaseListParams{BranchID: 2}); len(other) != 0 {
		t.Errorf("otra sucursal: %+v", other)
	}
}

// Toda compra en USD queda con el TC de su fecha de emisión: si no se envía, se completa con el TC guardado del día.
func TestPurchaseUSD_FillsExchangeRateFromSavedDailyRate(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	newOpenCashSession(t, db, 1, 1)
	pid := stockProduct(t, svc, "P-DAY")
	prev := dailyfx.Lookup
	dailyfx.Lookup = func(day string) (float64, bool, bool) { return 3.44, false, true }
	t.Cleanup(func() { dailyfx.Lookup = prev })

	p, err := svc.Create(usdInput(pid, "50", nil))
	if err != nil {
		t.Fatalf("USD sin TC con TC del día guardado debe completarse: %v", err)
	}
	if p.ExchangeRate == nil || *p.ExchangeRate != 3.44 {
		t.Errorf("TC de la compra = %v, quiere 3.44 (el guardado del día)", p.ExchangeRate)
	}
	dailyfx.Lookup = func(string) (float64, bool, bool) { return 0, false, false }
	if _, err := svc.Create(usdInput(pid, "51", nil)); err == nil {
		t.Error("sin TC del día y sin TC enviado debe rechazarse")
	}
}
