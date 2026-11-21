package service

import (
	"encoding/json"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type advEnv struct {
	db     *gorm.DB
	svc    *ProductService
	main   database.TenantBranch
	second database.TenantBranch
	userID uint
}

func setupAdvancedImportEnv(t *testing.T) *advEnv {
	t.Helper()
	db := setupProductServiceTestDB(t)
	if err := db.AutoMigrate(
		&database.TenantProductBranchPrice{},
		&database.TenantProductPresentationStock{},
		&database.TenantInventoryOperationType{},
	); err != nil {
		t.Fatal(err)
	}
	if err := database.SeedInventoryOperationTypes(db); err != nil {
		t.Fatal(err)
	}
	main := database.TenantBranch{Name: "Principal", Active: true, IsMain: true}
	second := database.TenantBranch{Name: "Sucursal Norte", Active: true}
	if err := db.Create(&main).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	return &advEnv{db: db, svc: NewProductService(db), main: main, second: second, userID: 1}
}

func f(v float64) *float64 { return &v }

func (e *advEnv) run(t *testing.T, items ...BulkImportAdvancedItem) *BulkImportAdvancedResult {
	t.Helper()
	res, err := e.svc.BulkImportAdvanced(items, e.main.ID, e.userID)
	if err != nil {
		t.Fatalf("BulkImportAdvanced: %v", err)
	}
	return res
}

func (e *advEnv) product(t *testing.T, code string) database.TenantProduct {
	t.Helper()
	var p database.TenantProduct
	if err := e.db.Where("code = ?", code).First(&p).Error; err != nil {
		t.Fatalf("producto %s: %v", code, err)
	}
	return p
}

func advProduct(row int, code, name string, price float64) BulkImportItem {
	return BulkImportItem{RowNumber: row, Name: name, Code: code, SalePrice: price}
}

// El JSON del endpoint aplana los campos del producto junto a las listas hijas.
func TestBulkImportAdvanced_JSONShape(t *testing.T) {
	raw := `{"row_number":2,"name":"Arroz","code":"ARR","sale_price":4.5,"min_stock":3,
	"sale_units":[{"row_number":2,"name":"Kilo","unit":"KGM","conversion_factor":1,"is_base":true,"price1":4.5}],
	"branch_prices":[{"row_number":2,"branch_name":"Principal","sale_price":5}]}`
	var it BulkImportAdvancedItem
	if err := json.Unmarshal([]byte(raw), &it); err != nil {
		t.Fatal(err)
	}
	if it.Code != "ARR" || it.MinStock == nil || *it.MinStock != 3 || len(it.SaleUnits) != 1 || !it.SaleUnits[0].IsBase || len(it.BranchPrices) != 1 {
		t.Fatalf("JSON mal interpretado: %+v", it)
	}
}

func TestBulkImportAdvanced_CreatesProductWithChildren(t *testing.T) {
	e := setupAdvancedImportEnv(t)
	res := e.run(t,
		BulkImportAdvancedItem{
			BulkImportItem: BulkImportItem{RowNumber: 2, Name: "Arroz", Code: "ARR", SalePrice: 4.5, Unit: "KGM", ManageStock: true, InitialStock: 100, MinStock: f(5)},
			SaleUnits: []BulkImportAdvancedSaleUnit{
				{RowNumber: 2, Name: "Kilo", Unit: "KGM", ConversionFactor: 1, IsBase: true, AllowFraction: true, Price1: 4.5},
				{RowNumber: 3, Name: "Gramo", Unit: "GRM", ConversionFactor: 0.001, AllowFraction: true, Price1: 0.0045},
				{RowNumber: 4, Name: "Saco 50 kg", Unit: "BX", ConversionFactor: 50, Price1: 210, Price2: f(200), Price3: f(190)},
			},
			BranchPrices: []BulkImportAdvancedBranchPrice{
				{RowNumber: 2, BranchName: "sucursal norte", SalePrice: 5},
				{RowNumber: 3, BranchName: "Sucursal Norte", SaleUnitName: "Kilo", SalePrice: 5.2},
			},
			BranchStocks: []BulkImportAdvancedBranchStock{
				{RowNumber: 2, BranchName: "Sucursal Norte", Quantity: 40},
			},
		},
		BulkImportAdvancedItem{
			BulkImportItem: BulkImportItem{RowNumber: 3, Name: "Gaseosa", Code: "GAS", SalePrice: 3},
			Presentations: []BulkImportAdvancedPresentation{
				{RowNumber: 2, Name: "Personal", SalePrice: 3},
				{RowNumber: 3, Name: "Familiar", SalePrice: 8},
			},
		},
	)
	if len(res.Failed) != 0 || res.Created != 2 {
		t.Fatalf("resultado: %+v", res)
	}
	if res.SaleUnitsCreated != 3 || res.BranchPricesCreated != 2 || res.PresentationsCreated != 2 || res.BranchStocksSet != 1 {
		t.Fatalf("contadores: %+v", res)
	}

	arroz := e.product(t, "ARR")
	if arroz.MinStock != 5 {
		t.Errorf("min_stock: %v", arroz.MinStock)
	}
	var units []database.TenantProductSaleUnit
	e.db.Where("product_id = ?", arroz.ID).Order("id").Find(&units)
	if len(units) != 3 || !units[0].IsBase || units[0].Unit != "KGM" || units[1].Unit != "GRM" {
		t.Fatalf("unidades: %+v", units)
	}
	if units[2].Price2 == nil || *units[2].Price2 != 200 || units[2].Price3 == nil || *units[2].Price3 != 190 {
		t.Errorf("precios 2/3 de la caja: %+v", units[2])
	}
	var pbp database.TenantProductBranchPrice
	if err := e.db.Where("product_id = ? AND branch_id = ?", arroz.ID, e.second.ID).First(&pbp).Error; err != nil || pbp.SalePrice != 5 || !pbp.Active {
		t.Errorf("precio de producto por sucursal: %+v err=%v", pbp, err)
	}
	var subp database.TenantProductSaleUnitBranchPrice
	if err := e.db.Where("sale_unit_id = ? AND branch_id = ?", units[0].ID, e.second.ID).First(&subp).Error; err != nil || subp.Price1 != 5.2 {
		t.Errorf("precio de unidad por sucursal: %+v err=%v", subp, err)
	}
	// Stock: 100 en la sucursal activa (columna stock_inicial) y 40 en la otra.
	var st database.TenantProductStock
	e.db.Where("product_id = ? AND branch_id = ?", arroz.ID, e.main.ID).First(&st)
	if st.Quantity != 100 {
		t.Errorf("stock principal: %v", st.Quantity)
	}
	st = database.TenantProductStock{}
	e.db.Where("product_id = ? AND branch_id = ?", arroz.ID, e.second.ID).First(&st)
	if st.Quantity != 40 {
		t.Errorf("stock norte: %v", st.Quantity)
	}
	var pres []database.TenantProductPresentation
	e.db.Where("product_id = ?", e.product(t, "GAS").ID).Find(&pres)
	if len(pres) != 2 {
		t.Errorf("presentaciones: %d", len(pres))
	}
}

func TestBulkImportAdvanced_CodeRequiredAndUnique(t *testing.T) {
	e := setupAdvancedImportEnv(t)
	res := e.run(t,
		BulkImportAdvancedItem{BulkImportItem: advProduct(2, "", "Sin código", 5)},
		BulkImportAdvancedItem{BulkImportItem: advProduct(3, "A1", "Uno", 5)},
		BulkImportAdvancedItem{BulkImportItem: advProduct(4, "a1", "Repetido", 5)},
	)
	if res.Created != 1 || len(res.Failed) != 2 {
		t.Fatalf("resultado: %+v", res)
	}
	if !strings.Contains(res.Failed[0].Error, "hoja Productos, fila 2") || !strings.Contains(res.Failed[1].Error, "repetido") {
		t.Errorf("errores: %+v", res.Failed)
	}
}

func TestBulkImportAdvanced_ReplaceDeactivatesMissingChildren(t *testing.T) {
	e := setupAdvancedImportEnv(t)
	first := BulkImportAdvancedItem{
		BulkImportItem: BulkImportItem{RowNumber: 2, Name: "Aceite", Code: "ACE", SalePrice: 9, Unit: "LTR"},
		SaleUnits: []BulkImportAdvancedSaleUnit{
			{RowNumber: 2, Name: "Litro", Unit: "LTR", ConversionFactor: 1, IsBase: true, Price1: 9},
			{RowNumber: 3, Name: "Caja x12", Unit: "BX", ConversionFactor: 12, Price1: 100},
		},
		BranchPrices: []BulkImportAdvancedBranchPrice{
			{RowNumber: 2, BranchName: "Principal", SalePrice: 9.5},
			{RowNumber: 3, BranchName: "Sucursal Norte", SalePrice: 10},
		},
	}
	if r := e.run(t, first); len(r.Failed) != 0 {
		t.Fatalf("alta: %+v", r.Failed)
	}

	// Segunda corrida: se cambia el precio del litro, desaparece la caja y un precio por sucursal.
	second := BulkImportAdvancedItem{
		BulkImportItem: BulkImportItem{RowNumber: 2, Name: "Aceite", Code: "ACE", SalePrice: 9, Unit: "LTR"},
		SaleUnits: []BulkImportAdvancedSaleUnit{
			{RowNumber: 2, Name: "litro", Unit: "LTR", ConversionFactor: 1, IsBase: true, Price1: 9.9},
		},
		BranchPrices: []BulkImportAdvancedBranchPrice{
			{RowNumber: 2, BranchName: "Principal", SalePrice: 9.7},
		},
	}
	// Otro producto que no aparece en las hojas hijas no debe tocarse.
	other := BulkImportAdvancedItem{BulkImportItem: advProduct(3, "OTR", "Otro", 1)}
	res := e.run(t, second, other)
	if len(res.Failed) != 0 || res.Updated != 1 || res.Created != 1 {
		t.Fatalf("resultado: %+v", res)
	}
	if res.SaleUnitsUpdated != 1 || res.SaleUnitsDeactivated != 1 || res.SaleUnitsCreated != 0 {
		t.Errorf("unidades: %+v", res)
	}
	if res.BranchPricesUpdated != 1 || res.BranchPricesDeactivated != 1 {
		t.Errorf("precios: %+v", res)
	}

	p := e.product(t, "ACE")
	var units []database.TenantProductSaleUnit
	e.db.Where("product_id = ?", p.ID).Order("id").Find(&units)
	if len(units) != 2 {
		t.Fatalf("no debe borrar físicamente: %d filas", len(units))
	}
	if !units[0].Active || units[0].Price1 != 9.9 || units[1].Active {
		t.Errorf("estado de unidades: %+v", units)
	}
	var norte database.TenantProductBranchPrice
	e.db.Where("product_id = ? AND branch_id = ?", p.ID, e.second.ID).First(&norte)
	if norte.Active {
		t.Error("el precio de Sucursal Norte debía quedar inactivo")
	}

	// Tercera corrida sin hojas hijas: no toca nada.
	res = e.run(t, BulkImportAdvancedItem{BulkImportItem: BulkImportItem{RowNumber: 2, Name: "Aceite", Code: "ACE", SalePrice: 9, Unit: "LTR"}})
	if res.SaleUnitsDeactivated != 0 || res.BranchPricesDeactivated != 0 {
		t.Errorf("sin hojas hijas no debe tocar: %+v", res)
	}
	var activeUnits int64
	e.db.Model(&database.TenantProductSaleUnit{}).Where("product_id = ? AND active = ?", p.ID, true).Count(&activeUnits)
	if activeUnits != 1 {
		t.Errorf("unidades activas: %d", activeUnits)
	}
}

func TestBulkImportAdvanced_SingleBaseUnitRule(t *testing.T) {
	e := setupAdvancedImportEnv(t)
	res := e.run(t, BulkImportAdvancedItem{
		BulkImportItem: advProduct(2, "X1", "Producto", 5),
		SaleUnits: []BulkImportAdvancedSaleUnit{
			{RowNumber: 2, Name: "A", Unit: "NIU", ConversionFactor: 1, IsBase: true, Price1: 5},
			{RowNumber: 3, Name: "B", Unit: "NIU", ConversionFactor: 1, IsBase: true, Price1: 5},
		},
	})
	if res.Created != 0 || len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Error, "hoja Unidades de venta, fila 3") {
		t.Fatalf("resultado: %+v", res)
	}
	// La base con factor distinto de 1 la rechaza validateSaleUnitInput (regla reutilizada) y revierte el producto.
	res = e.run(t, BulkImportAdvancedItem{
		BulkImportItem: advProduct(2, "X2", "Producto 2", 5),
		SaleUnits:      []BulkImportAdvancedSaleUnit{{RowNumber: 5, Name: "A", Unit: "NIU", ConversionFactor: 2, IsBase: true, Price1: 5}},
	})
	if res.Created != 0 || len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Error, "hoja Unidades de venta, fila 5") || !strings.Contains(res.Failed[0].Error, "factor") {
		t.Fatalf("resultado: %+v", res)
	}
	var n int64
	e.db.Model(&database.TenantProduct{}).Where("code = ?", "X2").Count(&n)
	if n != 0 {
		t.Error("rollback: el producto no debe quedar creado")
	}
}

func TestBulkImportAdvanced_FractionalUnitSameSunatCodeRejected(t *testing.T) {
	e := setupAdvancedImportEnv(t)
	res := e.run(t, BulkImportAdvancedItem{
		BulkImportItem: BulkImportItem{RowNumber: 2, Name: "Arroz", Code: "ARR", SalePrice: 4.5, Unit: "KGM"},
		SaleUnits: []BulkImportAdvancedSaleUnit{
			{RowNumber: 2, Name: "Gramo", Unit: "KGM", ConversionFactor: 0.001, Price1: 0.0045},
		},
	})
	if len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Error, "hoja Unidades de venta, fila 2") || !strings.Contains(res.Failed[0].Error, "GRM") {
		t.Fatalf("resultado: %+v", res)
	}
}

func TestBulkImportAdvanced_PresentationsAndSaleUnitsCannotCoexist(t *testing.T) {
	e := setupAdvancedImportEnv(t)
	// En el mismo Excel.
	res := e.run(t, BulkImportAdvancedItem{
		BulkImportItem: advProduct(2, "M1", "Mixto", 5),
		Presentations:  []BulkImportAdvancedPresentation{{RowNumber: 2, Name: "P", SalePrice: 5}},
		SaleUnits:      []BulkImportAdvancedSaleUnit{{RowNumber: 2, Name: "U", Unit: "NIU", ConversionFactor: 1, Price1: 5}},
	})
	if len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Error, "no pueden convivir") {
		t.Fatalf("mismo Excel: %+v", res)
	}
	// Contra lo que ya tiene el producto: tiene unidades de venta y se intentan presentaciones.
	e.run(t, BulkImportAdvancedItem{
		BulkImportItem: advProduct(2, "M2", "Con unidades", 5),
		SaleUnits:      []BulkImportAdvancedSaleUnit{{RowNumber: 2, Name: "U", Unit: "NIU", ConversionFactor: 1, Price1: 5}},
	})
	res = e.run(t, BulkImportAdvancedItem{
		BulkImportItem: advProduct(2, "M2", "Con unidades", 5),
		Presentations:  []BulkImportAdvancedPresentation{{RowNumber: 4, Name: "P", SalePrice: 5}},
	})
	if len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Error, "hoja Presentaciones, fila 4") {
		t.Fatalf("contra BD: %+v", res)
	}
	// Y al revés: tiene presentaciones activas y se intentan unidades de venta (CreateSaleUnit lo bloquea).
	e.run(t, BulkImportAdvancedItem{
		BulkImportItem: advProduct(2, "M3", "Con presentaciones", 5),
		Presentations:  []BulkImportAdvancedPresentation{{RowNumber: 2, Name: "P", SalePrice: 5}},
	})
	res = e.run(t, BulkImportAdvancedItem{
		BulkImportItem: advProduct(2, "M3", "Con presentaciones", 5),
		SaleUnits:      []BulkImportAdvancedSaleUnit{{RowNumber: 6, Name: "U", Unit: "NIU", ConversionFactor: 1, Price1: 5}},
	})
	if len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Error, "hoja Unidades de venta, fila 6") {
		t.Fatalf("unidades sobre presentaciones: %+v", res)
	}
}

func TestBulkImportAdvanced_PresentationsReplace(t *testing.T) {
	e := setupAdvancedImportEnv(t)
	e.run(t, BulkImportAdvancedItem{
		BulkImportItem: advProduct(2, "PZ", "Pizza", 10),
		Presentations: []BulkImportAdvancedPresentation{
			{RowNumber: 2, Name: "Personal", SalePrice: 10},
			{RowNumber: 3, Name: "Familiar", SalePrice: 30},
		},
	})
	res := e.run(t, BulkImportAdvancedItem{
		BulkImportItem: advProduct(2, "PZ", "Pizza", 10),
		Presentations:  []BulkImportAdvancedPresentation{{RowNumber: 2, Name: "personal", SalePrice: 12}},
	})
	if len(res.Failed) != 0 || res.PresentationsUpdated != 1 || res.PresentationsDeactivated != 1 || res.PresentationsCreated != 0 {
		t.Fatalf("resultado: %+v", res)
	}
	var active []database.TenantProductPresentation
	e.db.Where("product_id = ?", e.product(t, "PZ").ID).Find(&active)
	if len(active) != 1 || active[0].SalePrice != 12 {
		t.Errorf("presentaciones: %+v", active)
	}
}

func TestBulkImportAdvanced_BranchStockAbsoluteAdjustmentOnExisting(t *testing.T) {
	e := setupAdvancedImportEnv(t)
	item := func(qty float64) BulkImportAdvancedItem {
		return BulkImportAdvancedItem{
			BulkImportItem: BulkImportItem{RowNumber: 2, Name: "Lapicero", Code: "LAP", SalePrice: 1, ManageStock: true},
			BranchStocks:   []BulkImportAdvancedBranchStock{{RowNumber: 2, BranchName: "Sucursal Norte", Quantity: qty}},
		}
	}
	e.run(t, item(20))
	e.run(t, item(7)) // existente: ajuste absoluto (no suma)
	p := e.product(t, "LAP")
	var st database.TenantProductStock
	e.db.Where("product_id = ? AND branch_id = ?", p.ID, e.second.ID).First(&st)
	if st.Quantity != 7 {
		t.Fatalf("stock tras ajuste: %v, quería 7", st.Quantity)
	}
	e.run(t, item(0))
	e.db.Where("product_id = ? AND branch_id = ?", p.ID, e.second.ID).First(&st)
	if st.Quantity != 0 {
		t.Fatalf("stock tras 0: %v", st.Quantity)
	}
}

func TestBulkImportAdvanced_BranchStockValidations(t *testing.T) {
	e := setupAdvancedImportEnv(t)
	res := e.run(t,
		BulkImportAdvancedItem{
			BulkImportItem: advProduct(2, "S1", "Sin control", 1),
			BranchStocks:   []BulkImportAdvancedBranchStock{{RowNumber: 2, BranchName: "Principal", Quantity: 5}},
		},
		BulkImportAdvancedItem{
			BulkImportItem: BulkImportItem{RowNumber: 3, Name: "Sucursal mala", Code: "S2", SalePrice: 1, ManageStock: true},
			BranchStocks:   []BulkImportAdvancedBranchStock{{RowNumber: 4, BranchName: "Inexistente", Quantity: 5}},
		},
	)
	if len(res.Failed) != 2 || !strings.Contains(res.Failed[0].Error, "no controla stock") || !strings.Contains(res.Failed[1].Error, "hoja Stock por sucursal, fila 4") {
		t.Fatalf("resultado: %+v", res)
	}
}

func TestBulkImportAdvanced_RollbackWholeProductOnChildError(t *testing.T) {
	e := setupAdvancedImportEnv(t)
	// Una presentación válida y una unidad inexistente en precios por sucursal: nada debe quedar.
	res := e.run(t,
		BulkImportAdvancedItem{
			BulkImportItem: advProduct(2, "RB", "Rollback", 5),
			Presentations:  []BulkImportAdvancedPresentation{{RowNumber: 2, Name: "P", SalePrice: 5}},
			BranchPrices:   []BulkImportAdvancedBranchPrice{{RowNumber: 2, BranchName: "Principal", SaleUnitName: "Fantasma", SalePrice: 5}},
		},
		BulkImportAdvancedItem{BulkImportItem: advProduct(3, "OK", "Bueno", 5)},
	)
	if res.Created != 1 || len(res.Failed) != 1 || res.PresentationsCreated != 0 {
		t.Fatalf("resultado: %+v", res)
	}
	var n int64
	e.db.Model(&database.TenantProduct{}).Where("code = ?", "RB").Count(&n)
	if n != 0 {
		t.Error("el producto debió revertirse completo")
	}
	e.db.Model(&database.TenantProductPresentation{}).Count(&n)
	if n != 0 {
		t.Errorf("presentaciones huérfanas: %d", n)
	}
}

func TestBulkImportAdvanced_WithoutCostPermission(t *testing.T) {
	// El handler pone PurchasePrice=nil sin permiso de costo: un producto existente conserva su costo.
	e := setupAdvancedImportEnv(t)
	cost := 7.0
	e.run(t, BulkImportAdvancedItem{BulkImportItem: BulkImportItem{RowNumber: 2, Name: "Costoso", Code: "CST", SalePrice: 10, PurchasePrice: &cost}})
	e.run(t, BulkImportAdvancedItem{BulkImportItem: BulkImportItem{RowNumber: 2, Name: "Costoso", Code: "CST", SalePrice: 12, PurchasePrice: nil}})
	p := e.product(t, "CST")
	if p.PurchasePrice != 7 || p.SalePrice != 12 {
		t.Fatalf("costo=%v precio=%v", p.PurchasePrice, p.SalePrice)
	}
}

func TestBulkImportAdvanced_MaxItems(t *testing.T) {
	e := setupAdvancedImportEnv(t)
	items := make([]BulkImportAdvancedItem, BulkImportAdvancedMaxItems+1)
	if _, err := e.svc.BulkImportAdvanced(items, e.main.ID, 1); err == nil {
		t.Fatal("más de 200 productos debe rechazarse")
	}
}

// Importación básica: stock_minimo opcional (vacío conserva el existente).
func TestBulkImportCatalog_MinStock(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	svc.BulkImportCatalog([]BulkImportItem{{RowNumber: 2, Name: "A", Code: "MS1", SalePrice: 1, ManageStock: true, MinStock: f(4)}}, 0, 1)
	var p database.TenantProduct
	db.Where("code = ?", "MS1").First(&p)
	if p.MinStock != 4 {
		t.Fatalf("min_stock nuevo: %v", p.MinStock)
	}
	svc.BulkImportCatalog([]BulkImportItem{{RowNumber: 2, Name: "A", Code: "MS1", SalePrice: 2, ManageStock: true}}, 0, 1)
	db.Where("code = ?", "MS1").First(&p)
	if p.MinStock != 4 {
		t.Fatalf("vacío debe conservar: %v", p.MinStock)
	}
	svc.BulkImportCatalog([]BulkImportItem{{RowNumber: 2, Name: "A", Code: "MS1", SalePrice: 2, ManageStock: true, MinStock: f(9)}}, 0, 1)
	db.Where("code = ?", "MS1").First(&p)
	if p.MinStock != 9 {
		t.Fatalf("min_stock actualizado: %v", p.MinStock)
	}
}

// Dry-run (lo que hace el handler con dry_run=true): se ejecuta todo dentro de una transacción
// externa y se revierte; los contadores deben ser reales y no debe quedar nada en la BD.
func TestBulkImportAdvanced_DryRunLeavesNothing(t *testing.T) {
	e := setupAdvancedImportEnv(t)
	tx := e.db.Begin()
	res, err := NewProductService(tx).BulkImportAdvanced([]BulkImportAdvancedItem{{
		BulkImportItem: BulkImportItem{RowNumber: 2, Name: "Dry", Code: "DRY", SalePrice: 5, CategoryName: "Nueva categoría"},
		SaleUnits:      []BulkImportAdvancedSaleUnit{{RowNumber: 2, Name: "U", Unit: "NIU", ConversionFactor: 1, Price1: 5}},
	}}, e.main.ID, 1)
	tx.Rollback()
	if err != nil || res.Created != 1 || res.SaleUnitsCreated != 1 || len(res.Failed) != 0 {
		t.Fatalf("dry-run: %+v err=%v", res, err)
	}
	var n int64
	e.db.Model(&database.TenantProduct{}).Count(&n)
	var m int64
	e.db.Model(&database.TenantProductSaleUnit{}).Count(&m)
	var c int64
	e.db.Model(&database.TenantCategory{}).Count(&c)
	if n != 0 || m != 0 || c != 0 {
		t.Fatalf("el dry-run dejó datos: productos=%d unidades=%d categorías=%d", n, m, c)
	}
}
