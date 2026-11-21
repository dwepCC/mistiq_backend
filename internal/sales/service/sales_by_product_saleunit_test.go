package service

import (
	"fmt"
	"math"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupSalesByProductDB extiende setupSaleUnitIntegrationDB con TenantCategory (necesaria por el
// LEFT JOIN de salesByProductBaseQuery, que no se usa en el resto de tests de este paquete).
func setupSalesByProductDB(t *testing.T) *gorm.DB {
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
		&database.TenantProductSaleUnit{}, &database.TenantProductSaleUnitBranchPrice{}, &database.TenantProductBranchPrice{}, &database.TenantCategory{},
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

func sbpSeries(t *testing.T, db *gorm.DB) database.TenantDocumentSeries {
	t.Helper()
	s := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true,
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func sbpCreateSale(t *testing.T, db *gorm.DB, series database.TenantDocumentSeries, item SaleItemInput) *database.TenantSale {
	t.Helper()
	sale, err := NewSaleService(db).Create(CreateSaleInput{
		BranchID: 1, UserID: 1, SeriesID: series.ID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: item.UnitPrice * item.Quantity}},
		Items:    []SaleItemInput{item},
	})
	if err != nil {
		t.Fatalf("Create sale: %v", err)
	}
	return sale
}

func findRow(rows []SalesByProductRow, productID uint, saleUnitID *uint) *SalesByProductRow {
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

// Caso 1: producto SIN SaleUnit (legacy puro) → una sola fila, sale_unit_id nil, cantidad y
// precio promedio correctos.
func TestSalesByProduct_ProductWithoutSaleUnit_Legacy(t *testing.T) {
	db := setupSalesByProductDB(t)
	series := sbpSeries(t, db)
	p := database.TenantProduct{
		Code: "GAS", Name: "Gaseosa", Type: "product", Unit: "NIU", SalePrice: 3.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	pid := p.ID
	sbpCreateSale(t, db, series, SaleItemInput{ProductID: &pid, Quantity: 4, UnitPrice: 3.5, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true})

	rows, summary, err := NewSaleService(db).SalesByProduct(SalesByProductParams{})
	if err != nil {
		t.Fatalf("SalesByProduct: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.SaleUnitID != nil {
		t.Errorf("sale_unit_id = %v, want nil (legacy)", row.SaleUnitID)
	}
	if row.QuantitySold != 4 {
		t.Errorf("quantity_sold = %g, want 4", row.QuantitySold)
	}
	if math.Abs(row.AvgUnitPrice-3.5) > 1e-6 {
		t.Errorf("avg_unit_price = %.4f, want 3.50", row.AvgUnitPrice)
	}
	if summary.ProductsCount != 1 {
		t.Errorf("products_count = %d, want 1", summary.ProductsCount)
	}
}

// Caso 2: producto con UNA sola SaleUnit → una sola fila, sale_unit_id poblado, cantidad
// comercial (no base).
func TestSalesByProduct_ProductWithOneSaleUnit(t *testing.T) {
	db := setupSalesByProductDB(t)
	series := sbpSeries(t, db)
	p := database.TenantProduct{
		Code: "ARR", Name: "Arroz", Type: "product", Unit: "KGM", SalePrice: 4.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true,
	}
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
	sbpCreateSale(t, db, series, SaleItemInput{ProductID: &pid, SaleUnitID: &suid, Quantity: 2, UnitPrice: 450, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})

	rows, _, err := NewSaleService(db).SalesByProduct(SalesByProductParams{})
	if err != nil {
		t.Fatalf("SalesByProduct: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.SaleUnitID == nil || *row.SaleUnitID != suid {
		t.Errorf("sale_unit_id = %v, want %d", row.SaleUnitID, suid)
	}
	// Comercial: 2 Sacos, NO 200 KG (nunca convertido en el reporte).
	if row.QuantitySold != 2 {
		t.Errorf("quantity_sold = %g, want 2 (comercial, no base)", row.QuantitySold)
	}
}

// Caso 3: mismo producto vendido con DOS SaleUnits distintas → 2 filas separadas, cantidades
// NUNCA mezcladas, precio promedio correcto por combinación.
func TestSalesByProduct_SameProductTwoSaleUnits_NeverMixed(t *testing.T) {
	db := setupSalesByProductDB(t)
	series := sbpSeries(t, db)
	p := database.TenantProduct{
		Code: "ARR", Name: "Arroz", Type: "product", Unit: "KGM", SalePrice: 4.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true,
	}
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
	docena := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Bolsa 5 KG", ConversionFactor: 5, AllowFraction: true, Price1: 25, Active: true}
	if err := db.Create(&docena).Error; err != nil {
		t.Fatal(err)
	}
	pid, sacoID, bolsaID := p.ID, saco.ID, docena.ID
	// 3 Sacos (comercial=3) + 10 Bolsas (comercial=10) — NUNCA debe verse como "13".
	sbpCreateSale(t, db, series, SaleItemInput{ProductID: &pid, SaleUnitID: &sacoID, Quantity: 3, UnitPrice: 450, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})
	sbpCreateSale(t, db, series, SaleItemInput{ProductID: &pid, SaleUnitID: &bolsaID, Quantity: 10, UnitPrice: 25, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})

	rows, summary, err := NewSaleService(db).SalesByProduct(SalesByProductParams{})
	if err != nil {
		t.Fatalf("SalesByProduct: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (una por SaleUnit, nunca fusionadas)", len(rows))
	}
	sacoRow := findRow(rows, pid, &sacoID)
	bolsaRow := findRow(rows, pid, &bolsaID)
	if sacoRow == nil || bolsaRow == nil {
		t.Fatalf("no se encontraron ambas filas por SaleUnit: saco=%v bolsa=%v", sacoRow, bolsaRow)
	}
	if sacoRow.QuantitySold != 3 {
		t.Errorf("Saco: quantity_sold = %g, want 3", sacoRow.QuantitySold)
	}
	if bolsaRow.QuantitySold != 10 {
		t.Errorf("Bolsa: quantity_sold = %g, want 10", bolsaRow.QuantitySold)
	}
	// Suma prohibida: en ningún lado debe aparecer 13.
	for _, r := range rows {
		if r.QuantitySold == 13 {
			t.Fatalf("se encontró una fila con quantity_sold=13 — cantidades de distintas SaleUnits quedaron mezcladas")
		}
	}
	// Precio promedio por combinación, no global.
	if math.Abs(sacoRow.AvgUnitPrice-450) > 1e-6 {
		t.Errorf("Saco: avg_unit_price = %.2f, want 450.00", sacoRow.AvgUnitPrice)
	}
	if math.Abs(bolsaRow.AvgUnitPrice-25) > 1e-6 {
		t.Errorf("Bolsa: avg_unit_price = %.2f, want 25.00", bolsaRow.AvgUnitPrice)
	}
	// Total monetario SÍ se consolida por producto (son soles, magnitud común).
	wantTotal := sacoRow.TotalAmount + bolsaRow.TotalAmount
	if math.Abs(summary.TotalAmount-wantTotal) > 1e-6 {
		t.Errorf("summary.TotalAmount = %.2f, want %.2f (suma de ambas combinaciones)", summary.TotalAmount, wantTotal)
	}
	// products_count = productos DISTINTOS (1), no filas (2).
	if summary.ProductsCount != 1 {
		t.Errorf("products_count = %d, want 1 (un solo producto, aunque tenga 2 filas)", summary.ProductsCount)
	}
}

// Caso 4: mismo producto con una venta por SaleUnit y otra legacy (sale_unit_id nil) → 2 filas
// separadas, la legacy nunca se fusiona con la de la SaleUnit.
func TestSalesByProduct_SaleUnitPlusLegacy_NeverMixed(t *testing.T) {
	db := setupSalesByProductDB(t)
	series := sbpSeries(t, db)
	p := database.TenantProduct{
		Code: "ARR", Name: "Arroz", Type: "product", Unit: "KGM", SalePrice: 4.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true,
	}
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
	sbpCreateSale(t, db, series, SaleItemInput{ProductID: &pid, SaleUnitID: &sacoID, Quantity: 1, UnitPrice: 450, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})
	// Venta legacy: 5 KG sueltos, sin SaleUnit.
	sbpCreateSale(t, db, series, SaleItemInput{ProductID: &pid, Quantity: 5, UnitPrice: 4.5, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})

	rows, _, err := NewSaleService(db).SalesByProduct(SalesByProductParams{})
	if err != nil {
		t.Fatalf("SalesByProduct: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (SaleUnit y legacy nunca deben fusionarse)", len(rows))
	}
	sacoRow := findRow(rows, pid, &sacoID)
	legacyRow := findRow(rows, pid, nil)
	if sacoRow == nil || legacyRow == nil {
		t.Fatalf("no se encontraron ambas filas: saco=%v legacy=%v", sacoRow, legacyRow)
	}
	if sacoRow.QuantitySold != 1 {
		t.Errorf("Saco: quantity_sold = %g, want 1", sacoRow.QuantitySold)
	}
	if legacyRow.QuantitySold != 5 {
		t.Errorf("Legacy: quantity_sold = %g, want 5", legacyRow.QuantitySold)
	}
}

// Verifica el orden: categoría, luego producto (agrupa las filas del mismo producto entre sí),
// y dentro del mismo producto por monto descendente — decisión explícita del usuario en 7J.
func TestSalesByProduct_SortGroupsSameProductTogether(t *testing.T) {
	db := setupSalesByProductDB(t)
	series := sbpSeries(t, db)
	p := database.TenantProduct{
		Code: "ARR", Name: "Arroz", Type: "product", Unit: "KGM", SalePrice: 4.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true,
	}
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
	// Otro producto con mayor monto individual, para confirmar que no se intercala entre las
	// filas del Arroz.
	other := database.TenantProduct{
		Code: "TV", Name: "Televisor", Type: "product", Unit: "NIU", SalePrice: 2000,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, BranchID: 1, Active: true,
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	pid, sacoID, otherID := p.ID, saco.ID, other.ID
	sbpCreateSale(t, db, series, SaleItemInput{ProductID: &pid, SaleUnitID: &sacoID, Quantity: 1, UnitPrice: 450, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})
	sbpCreateSale(t, db, series, SaleItemInput{ProductID: &pid, Quantity: 5, UnitPrice: 4.5, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true})
	sbpCreateSale(t, db, series, SaleItemInput{ProductID: &otherID, Quantity: 1, UnitPrice: 2000, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true})

	rows, _, err := NewSaleService(db).SalesByProduct(SalesByProductParams{})
	if err != nil {
		t.Fatalf("SalesByProduct: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	// Las 2 filas de Arroz deben quedar consecutivas (agrupadas), sin que Televisor (monto mayor)
	// se intercale entre ellas.
	arrozIdx := []int{}
	for i, r := range rows {
		if r.ProductID == pid {
			arrozIdx = append(arrozIdx, i)
		}
	}
	if len(arrozIdx) != 2 || arrozIdx[1] != arrozIdx[0]+1 {
		t.Fatalf("las filas de Arroz no quedaron consecutivas: índices %v (total filas: %d)", arrozIdx, len(rows))
	}
	// Dentro de Arroz, el Saco (S/450) debe ir antes que el legacy (S/22.50) — monto descendente.
	if rows[arrozIdx[0]].TotalAmount < rows[arrozIdx[1]].TotalAmount {
		t.Errorf("dentro del mismo producto no quedó ordenado por monto descendente: %.2f antes de %.2f", rows[arrozIdx[0]].TotalAmount, rows[arrozIdx[1]].TotalAmount)
	}
}
