package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupPresentationReportTestDB: universo mínimo para ListReport con productos con y sin
// presentaciones — mismas tablas que consume enrichReport (Fase 2, Contrato ecommerce v2 §1.8).
func setupPresentationReportTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantProduct{}, &database.TenantCategory{}, &database.TenantBranch{},
		&database.TenantProductStock{}, &database.TenantProductPresentation{},
		&database.TenantProductPresentationStock{}, &database.TenantProductSerial{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestListReport_ProductoSimple_SinPresentaciones: caso 1 del checklist de Fase 2 — un producto
// sin variantes sigue funcionando exactamente igual, Presentations queda vacío/nil.
func TestListReport_ProductoSimple_SinPresentaciones(t *testing.T) {
	db := setupPresentationReportTestDB(t)
	branch := database.TenantBranch{Name: "Principal", IsMain: true}
	db.Create(&branch)
	product := database.TenantProduct{
		Code: "SIMPLE1", Name: "Producto simple", Type: "product", Unit: "NIU", SalePrice: 20,
		ManageStock: true, HasVariants: false, Active: true, ShowInDigitalCatalog: true,
	}
	db.Create(&product)
	db.Create(&database.TenantProductStock{ProductID: product.ID, BranchID: branch.ID, Quantity: 8})

	svc := NewProductService(db)
	// Sin Limit, ListReport no calcula total (comportamiento existente, sin cambios de esta fase)
	// — solo se verifica la lista.
	items, _, err := svc.ListReport(ProductListParams{ActiveOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("se esperaba 1 producto, hay %d", len(items))
	}
	if len(items[0].Presentations) != 0 {
		t.Fatalf("producto sin variantes no debe tener presentaciones, tiene %d", len(items[0].Presentations))
	}
	if items[0].StockTotal != 8 {
		t.Fatalf("stock_total = %.2f, se esperaba 8 (comportamiento sin cambios)", items[0].StockTotal)
	}
}

// TestListReport_ProductoConUnaPresentacion: caso 2.
func TestListReport_ProductoConUnaPresentacion(t *testing.T) {
	db := setupPresentationReportTestDB(t)
	branch := database.TenantBranch{Name: "Principal", IsMain: true}
	db.Create(&branch)
	product := database.TenantProduct{
		Code: "VAR1", Name: "Polo con talla", Type: "product", Unit: "NIU", SalePrice: 25,
		ManageStock: true, HasVariants: true, Active: true, ShowInDigitalCatalog: true,
	}
	db.Create(&product)
	pres := database.TenantProductPresentation{ProductID: product.ID, Name: "M", SalePrice: 27, Active: true}
	db.Create(&pres)
	db.Create(&database.TenantProductPresentationStock{PresentationID: pres.ID, BranchID: branch.ID, Quantity: 5})

	svc := NewProductService(db)
	items, _, err := svc.ListReport(ProductListParams{ActiveOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items[0].Presentations) != 1 {
		t.Fatalf("se esperaba 1 presentación, hay %d", len(items[0].Presentations))
	}
	p := items[0].Presentations[0]
	if p.ID != pres.ID || p.Name != "M" || p.SalePrice != 27 || p.Stock != 5 {
		t.Fatalf("presentación incorrecta: %+v", p)
	}
	// El stock del producto (agregado) también debe reflejar el de la presentación, sin duplicar
	// lógica: es la MISMA cuenta que ya hacía enrichReport para productos con variantes.
	if items[0].StockTotal != 5 {
		t.Fatalf("stock_total = %.2f, se esperaba 5", items[0].StockTotal)
	}
}

// TestListReport_ProductoConMultiplesPresentaciones: caso 3, incluye combinaciones con nombre
// compuesto (Contrato v2 §1.8: "Rojo / XL" en vez de atributos combinables).
func TestListReport_ProductoConMultiplesPresentaciones(t *testing.T) {
	db := setupPresentationReportTestDB(t)
	branchA := database.TenantBranch{Name: "Sucursal A", IsMain: true}
	db.Create(&branchA)
	branchB := database.TenantBranch{Name: "Sucursal B"}
	db.Create(&branchB)

	product := database.TenantProduct{
		Code: "VARMULTI", Name: "Polo básico", Type: "product", Unit: "NIU", SalePrice: 30,
		ManageStock: true, HasVariants: true, Active: true, ShowInDigitalCatalog: true,
	}
	db.Create(&product)

	rojoS := database.TenantProductPresentation{ProductID: product.ID, Name: "Rojo / S", SalePrice: 30, SortOrder: 1, Active: true}
	rojoM := database.TenantProductPresentation{ProductID: product.ID, Name: "Rojo / M", SalePrice: 30, SortOrder: 2, Active: true}
	azulL := database.TenantProductPresentation{ProductID: product.ID, Name: "Azul / L", SalePrice: 32, SortOrder: 3, Active: true}
	inactiva := database.TenantProductPresentation{ProductID: product.ID, Name: "Descontinuada", SalePrice: 30, SortOrder: 4}
	db.Create(&rojoS)
	db.Create(&rojoM)
	db.Create(&azulL)
	db.Create(&inactiva)
	// gorm:"default:true" en Active hace que GORM omita el false explícito del INSERT (mismo
	// caso documentado en TenantUnit.Active) — se desactiva con un UPDATE aparte, igual que hace
	// el propio código de producción al editar una presentación existente.
	db.Model(&inactiva).Update("active", false)

	db.Create(&database.TenantProductPresentationStock{PresentationID: rojoS.ID, BranchID: branchA.ID, Quantity: 3})
	db.Create(&database.TenantProductPresentationStock{PresentationID: rojoS.ID, BranchID: branchB.ID, Quantity: 2})
	db.Create(&database.TenantProductPresentationStock{PresentationID: rojoM.ID, BranchID: branchA.ID, Quantity: 0})
	db.Create(&database.TenantProductPresentationStock{PresentationID: azulL.ID, BranchID: branchA.ID, Quantity: 7})

	svc := NewProductService(db)
	items, _, err := svc.ListReport(ProductListParams{ActiveOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	pres := items[0].Presentations
	// Presentación inactiva ("Descontinuada") no debe aparecer — mismo filtro que ya usa
	// ListProductPresentations/POS (active=true).
	if len(pres) != 3 {
		t.Fatalf("se esperaban 3 presentaciones activas, hay %d: %+v", len(pres), pres)
	}
	byName := map[string]PresentationOption{}
	for _, p := range pres {
		byName[p.Name] = p
	}
	if byName["Rojo / S"].Stock != 5 { // 3 + 2, agregado de ambas sucursales
		t.Errorf("Rojo / S stock = %.2f, se esperaba 5 (agregado de todas las sucursales)", byName["Rojo / S"].Stock)
	}
	if byName["Rojo / M"].Stock != 0 {
		t.Errorf("Rojo / M stock = %.2f, se esperaba 0", byName["Rojo / M"].Stock)
	}
	if byName["Azul / L"].Stock != 7 || byName["Azul / L"].SalePrice != 32 {
		t.Errorf("Azul / L = %+v, precio/stock incorrecto", byName["Azul / L"])
	}
	// El total del producto debe sumar TODAS las presentaciones, no solo una.
	if items[0].StockTotal != 12 { // 5 + 0 + 7
		t.Fatalf("stock_total = %.2f, se esperaba 12", items[0].StockTotal)
	}
}

// TestListReport_PresentationStockRespetaBranchID: la disponibilidad debe respetar el
// alcance/sucursal pedido — mismo criterio que ya aplica StockTotal/StockByBranch hoy.
func TestListReport_PresentationStockRespetaBranchID(t *testing.T) {
	db := setupPresentationReportTestDB(t)
	branchA := database.TenantBranch{Name: "A", IsMain: true}
	db.Create(&branchA)
	branchB := database.TenantBranch{Name: "B"}
	db.Create(&branchB)

	product := database.TenantProduct{Code: "BR1", Name: "Producto sucursal", Type: "product", Unit: "NIU", SalePrice: 10, ManageStock: true, HasVariants: true, Active: true}
	db.Create(&product)
	pres := database.TenantProductPresentation{ProductID: product.ID, Name: "Única", SalePrice: 10, Active: true}
	db.Create(&pres)
	db.Create(&database.TenantProductPresentationStock{PresentationID: pres.ID, BranchID: branchA.ID, Quantity: 4})
	db.Create(&database.TenantProductPresentationStock{PresentationID: pres.ID, BranchID: branchB.ID, Quantity: 6})

	svc := NewProductService(db)
	items, _, err := svc.ListReport(ProductListParams{ActiveOnly: true, BranchID: branchA.ID})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Presentations[0].Stock != 4 {
		t.Fatalf("con branch_id=%d, stock = %.2f, se esperaba 4 (solo esa sucursal)", branchA.ID, items[0].Presentations[0].Stock)
	}
}
