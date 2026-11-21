package handler

import (
	"fmt"
	"math"
	"testing"
	"time"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupTopProductsDB monta lo mínimo para crear ventas reales (vía SaleService.Create, no
// inserts directos) y llamar a computeTopProducts — mismo patrón que
// internal/sales/service/sales_by_product_saleunit_test.go (Fase 7J.1).
func setupTopProductsDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantCompanyConfig{}, &database.TenantDocumentSeries{}, &database.TenantContact{},
		&database.TenantSale{}, &database.TenantSaleItem{}, &database.TenantSalePayment{},
		&database.TenantCashSession{}, &database.TenantPaymentMethod{}, &database.TenantProduct{},
		&database.TenantBranch{}, &database.TenantProductStock{}, &database.TenantStockMovement{},
		&database.TenantInventoryOperationType{}, &database.TenantProductSerial{},
		&database.TenantProductSaleUnit{}, &database.TenantProductSaleUnitBranchPrice{}, &database.TenantProductBranchPrice{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.TenantCompanyConfig{ID: 1, SunatEnabled: true, TaxRate: 18}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.SeedInventoryOperationTypes(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCashSession{
		BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func tpSeries(t *testing.T, db *gorm.DB) database.TenantDocumentSeries {
	t.Helper()
	s := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true,
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func tpCreateSale(t *testing.T, db *gorm.DB, series database.TenantDocumentSeries, issueDate time.Time, item salessvc.SaleItemInput) {
	t.Helper()
	_, err := salessvc.NewSaleService(db).Create(salessvc.CreateSaleInput{
		BranchID: 1, UserID: 1, SeriesID: series.ID, DocType: "00",
		IssueDate: issueDate, Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []salessvc.PaymentInput{{Method: "cash", Amount: item.UnitPrice * item.Quantity}},
		Items:    []salessvc.SaleItemInput{item},
	})
	if err != nil {
		t.Fatalf("Create sale: %v", err)
	}
}

func findTopProductRow(rows []dashboardTopProductRow, productID uint, saleUnitID *uint) *dashboardTopProductRow {
	for i := range rows {
		if rows[i].ProductID != productID {
			continue
		}
		if saleUnitID == nil && rows[i].SaleUnitID == nil {
			return &rows[i]
		}
		if saleUnitID != nil && rows[i].SaleUnitID != nil && *rows[i].SaleUnitID == *saleUnitID {
			return &rows[i]
		}
	}
	return nil
}

const tpLimit = 10

// Caso 1: producto sin SaleUnit → una sola fila, sale_unit_id nil.
func TestComputeTopProducts_WithoutSaleUnit(t *testing.T) {
	db := setupTopProductsDB(t)
	series := tpSeries(t, db)
	p := database.TenantProduct{Code: "GAS", Name: "Gaseosa", Type: "product", Unit: "NIU", SalePrice: 3.5, IgvAffectationType: "10", PriceIncludesIgv: true, BranchID: 1, Active: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	pid := p.ID
	now := time.Now()
	tpCreateSale(t, db, series, now, salessvc.SaleItemInput{ProductID: &pid, Quantity: 4, UnitPrice: 3.5, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true})

	rows, err := computeTopProducts(db, now.Add(-time.Hour), now.Add(time.Hour), 0, 0, false, tpLimit)
	if err != nil {
		t.Fatalf("computeTopProducts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].SaleUnitID != nil {
		t.Errorf("sale_unit_id = %v, want nil", rows[0].SaleUnitID)
	}
	if rows[0].Qty != 4 {
		t.Errorf("quantity = %g, want 4", rows[0].Qty)
	}
}

// Caso 2: producto con UNA SaleUnit → una fila, cantidad comercial (no base).
func TestComputeTopProducts_OneSaleUnit(t *testing.T) {
	db := setupTopProductsDB(t)
	series := tpSeries(t, db)
	p := database.TenantProduct{Code: "ARR", Name: "Arroz", Type: "product", Unit: "KGM", SalePrice: 4.5, IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 1000}).Error; err != nil {
		t.Fatal(err)
	}
	saco := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Saco 100 KG", ConversionFactor: 100, AllowFraction: true, Price1: 450, Active: true}
	if err := db.Create(&saco).Error; err != nil {
		t.Fatal(err)
	}
	pid, suid := p.ID, saco.ID
	now := time.Now()
	tpCreateSale(t, db, series, now, salessvc.SaleItemInput{ProductID: &pid, SaleUnitID: &suid, Quantity: 2, UnitPrice: 450, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})

	rows, err := computeTopProducts(db, now.Add(-time.Hour), now.Add(time.Hour), 0, 0, false, tpLimit)
	if err != nil {
		t.Fatalf("computeTopProducts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].SaleUnitID == nil || *rows[0].SaleUnitID != suid {
		t.Errorf("sale_unit_id = %v, want %d", rows[0].SaleUnitID, suid)
	}
	if rows[0].Qty != 2 {
		t.Errorf("quantity = %g, want 2 (comercial, no 200 base)", rows[0].Qty)
	}
}

// Caso 3: mismo producto con DOS SaleUnits → 2 filas, cantidades NUNCA mezcladas (nunca "13").
func TestComputeTopProducts_TwoSaleUnits_NeverMixed(t *testing.T) {
	db := setupTopProductsDB(t)
	series := tpSeries(t, db)
	p := database.TenantProduct{Code: "ARR", Name: "Arroz", Type: "product", Unit: "KGM", SalePrice: 4.5, IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 10000}).Error; err != nil {
		t.Fatal(err)
	}
	saco := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Saco 100 KG", ConversionFactor: 100, AllowFraction: true, Price1: 450, Active: true}
	if err := db.Create(&saco).Error; err != nil {
		t.Fatal(err)
	}
	bolsa := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Bolsa 5 KG", ConversionFactor: 5, AllowFraction: true, Price1: 25, Active: true}
	if err := db.Create(&bolsa).Error; err != nil {
		t.Fatal(err)
	}
	pid, sacoID, bolsaID := p.ID, saco.ID, bolsa.ID
	now := time.Now()
	tpCreateSale(t, db, series, now, salessvc.SaleItemInput{ProductID: &pid, SaleUnitID: &sacoID, Quantity: 3, UnitPrice: 450, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})
	tpCreateSale(t, db, series, now, salessvc.SaleItemInput{ProductID: &pid, SaleUnitID: &bolsaID, Quantity: 10, UnitPrice: 25, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})

	rows, err := computeTopProducts(db, now.Add(-time.Hour), now.Add(time.Hour), 0, 0, false, tpLimit)
	if err != nil {
		t.Fatalf("computeTopProducts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	sacoRow := findTopProductRow(rows, pid, &sacoID)
	bolsaRow := findTopProductRow(rows, pid, &bolsaID)
	if sacoRow == nil || bolsaRow == nil {
		t.Fatalf("no se encontraron ambas filas: saco=%v bolsa=%v", sacoRow, bolsaRow)
	}
	if sacoRow.Qty != 3 {
		t.Errorf("Saco: quantity = %g, want 3", sacoRow.Qty)
	}
	if bolsaRow.Qty != 10 {
		t.Errorf("Bolsa: quantity = %g, want 10", bolsaRow.Qty)
	}
	for _, r := range rows {
		if r.Qty == 13 {
			t.Fatalf("se encontró quantity=13 — cantidades de distintas SaleUnits quedaron mezcladas")
		}
	}
	wantTotal := sacoRow.Total + bolsaRow.Total
	if math.Abs((sacoRow.Total+bolsaRow.Total)-wantTotal) > 1e-6 {
		t.Errorf("suma de montos inesperada")
	}
}

// Caso 4: SaleUnit + legacy del mismo producto → 2 filas, nunca fusionadas.
func TestComputeTopProducts_SaleUnitPlusLegacy_NeverMixed(t *testing.T) {
	db := setupTopProductsDB(t)
	series := tpSeries(t, db)
	p := database.TenantProduct{Code: "ARR", Name: "Arroz", Type: "product", Unit: "KGM", SalePrice: 4.5, IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 10000}).Error; err != nil {
		t.Fatal(err)
	}
	saco := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Saco 100 KG", ConversionFactor: 100, AllowFraction: true, Price1: 450, Active: true}
	if err := db.Create(&saco).Error; err != nil {
		t.Fatal(err)
	}
	pid, sacoID := p.ID, saco.ID
	now := time.Now()
	tpCreateSale(t, db, series, now, salessvc.SaleItemInput{ProductID: &pid, SaleUnitID: &sacoID, Quantity: 1, UnitPrice: 450, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})
	tpCreateSale(t, db, series, now, salessvc.SaleItemInput{ProductID: &pid, Quantity: 5, UnitPrice: 4.5, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})

	rows, err := computeTopProducts(db, now.Add(-time.Hour), now.Add(time.Hour), 0, 0, false, tpLimit)
	if err != nil {
		t.Fatalf("computeTopProducts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (SaleUnit y legacy nunca deben fusionarse)", len(rows))
	}
	sacoRow := findTopProductRow(rows, pid, &sacoID)
	legacyRow := findTopProductRow(rows, pid, nil)
	if sacoRow == nil || legacyRow == nil {
		t.Fatalf("no se encontraron ambas filas: saco=%v legacy=%v", sacoRow, legacyRow)
	}
	if sacoRow.Qty != 1 || legacyRow.Qty != 5 {
		t.Errorf("cantidades incorrectas: saco=%g (want 1) legacy=%g (want 5)", sacoRow.Qty, legacyRow.Qty)
	}
}

// El "top N" se aplica DESPUÉS del desglose, sobre combinaciones — un mismo producto puede ocupar
// más de una posición del ranking. Documentado explícitamente (Fase 7J.2): con limit=1, solo
// queda la combinación de mayor monto, aunque ambas sean del mismo producto.
func TestComputeTopProducts_LimitAppliesAfterSplit(t *testing.T) {
	db := setupTopProductsDB(t)
	series := tpSeries(t, db)
	p := database.TenantProduct{Code: "ARR", Name: "Arroz", Type: "product", Unit: "KGM", SalePrice: 4.5, IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 10000}).Error; err != nil {
		t.Fatal(err)
	}
	saco := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Saco 100 KG", ConversionFactor: 100, AllowFraction: true, Price1: 450, Active: true}
	if err := db.Create(&saco).Error; err != nil {
		t.Fatal(err)
	}
	pid, sacoID := p.ID, saco.ID
	now := time.Now()
	tpCreateSale(t, db, series, now, salessvc.SaleItemInput{ProductID: &pid, SaleUnitID: &sacoID, Quantity: 1, UnitPrice: 450, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})
	tpCreateSale(t, db, series, now, salessvc.SaleItemInput{ProductID: &pid, Quantity: 5, UnitPrice: 4.5, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})

	rows, err := computeTopProducts(db, now.Add(-time.Hour), now.Add(time.Hour), 0, 0, false, 1)
	if err != nil {
		t.Fatalf("computeTopProducts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (limit aplicado sobre combinaciones)", len(rows))
	}
	if rows[0].SaleUnitID == nil || *rows[0].SaleUnitID != sacoID {
		t.Errorf("con limit=1 debía quedar la combinación de mayor monto (Saco, S/450), got sale_unit_id=%v total=%.2f", rows[0].SaleUnitID, rows[0].Total)
	}
}
