package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var publicProductsTestDBCounter int

func setupPublicProductsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// Sufijo incremental: t.Name() por sí solo repite el mismo nombre para dos llamadas dentro del
	// MISMO test (ej. TestPublicProducts_TenantIsolation crea dos "tenants") — con cache=shared,
	// dos DSN iguales apuntan literalmente a la misma base en memoria, no a dos bases distintas.
	publicProductsTestDBCounter++
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), publicProductsTestDBCounter)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
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

// TestPublicProducts_ExponePresentacionesDeProductoConVariantes: contrato esperado de Fase 2 — la
// API pública debe traer presentation_id/name/sale_price/stock por producto con variantes.
func TestPublicProducts_ExponePresentacionesDeProductoConVariantes(t *testing.T) {
	db := setupPublicProductsTestDB(t)
	branch := database.TenantBranch{Name: "Principal", IsMain: true}
	db.Create(&branch)
	product := database.TenantProduct{
		Code: "PUB-VAR", Name: "Polo público", Type: "product", Unit: "NIU", SalePrice: 25,
		ManageStock: true, HasVariants: true, Active: true, ShowInDigitalCatalog: true,
	}
	db.Create(&product)
	rojo := database.TenantProductPresentation{ProductID: product.ID, Name: "Rojo / M", SalePrice: 27, Active: true}
	db.Create(&rojo)
	db.Create(&database.TenantProductPresentationStock{PresentationID: rojo.ID, BranchID: branch.ID, Quantity: 4})

	svc := &EcommerceService{db: db}
	items, _, err := svc.PublicProducts("", 0, nil, nil, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("se esperaba 1 producto, hay %d", len(items))
	}
	if len(items[0].Presentations) != 1 {
		t.Fatalf("se esperaba 1 presentación expuesta, hay %d", len(items[0].Presentations))
	}
	p := items[0].Presentations[0]
	if p.ID != rojo.ID || p.Name != "Rojo / M" || p.SalePrice != 27 || p.Stock != 4 {
		t.Fatalf("presentación pública incorrecta: %+v", p)
	}
}

// TestPublicProducts_ProductoSimple_SinPresentaciones: caso 1 del checklist — sigue funcionando
// exactamente igual que antes de esta fase.
func TestPublicProducts_ProductoSimple_SinPresentaciones(t *testing.T) {
	db := setupPublicProductsTestDB(t)
	branch := database.TenantBranch{Name: "Principal", IsMain: true}
	db.Create(&branch)
	product := database.TenantProduct{
		Code: "PUB-SIMPLE", Name: "Producto simple público", Type: "product", Unit: "NIU", SalePrice: 15,
		ManageStock: true, HasVariants: false, Active: true, ShowInDigitalCatalog: true,
	}
	db.Create(&product)
	db.Create(&database.TenantProductStock{ProductID: product.ID, BranchID: branch.ID, Quantity: 10})

	svc := &EcommerceService{db: db}
	items, _, err := svc.PublicProducts("", 0, nil, nil, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items[0].Presentations) != 0 {
		t.Fatalf("producto simple no debe traer presentaciones, trae %d", len(items[0].Presentations))
	}
	if items[0].StockTotal != 10 {
		t.Fatalf("stock_total = %.2f, se esperaba 10 (flujo de producto simple sin cambios)", items[0].StockTotal)
	}
}

// TestPublicProducts_ShowStockFalse_OcultaStockPeroConservaPresentaciones: "Mostrar stock" en
// false debe seguir ocultando el número de disponibilidad (mismo criterio que StockTotal), pero
// las presentaciones (identidad para poder elegir) deben seguir viniendo — sin selección, el
// cliente no puede armar el carrito, sin importar la config de stock.
func TestPublicProducts_ShowStockFalse_OcultaStockPeroConservaPresentaciones(t *testing.T) {
	db := setupPublicProductsTestDB(t)
	branch := database.TenantBranch{Name: "Principal", IsMain: true}
	db.Create(&branch)
	product := database.TenantProduct{
		Code: "PUB-NOSTOCK", Name: "Polo sin stock visible", Type: "product", Unit: "NIU", SalePrice: 20,
		ManageStock: true, HasVariants: true, Active: true, ShowInDigitalCatalog: true,
	}
	db.Create(&product)
	pres := database.TenantProductPresentation{ProductID: product.ID, Name: "Única", SalePrice: 20, Active: true}
	db.Create(&pres)
	db.Create(&database.TenantProductPresentationStock{PresentationID: pres.ID, BranchID: branch.ID, Quantity: 9})

	svc := &EcommerceService{db: db}
	items, _, err := svc.PublicProducts("", 0, nil, nil, 0, 0, false) // showStock=false
	if err != nil {
		t.Fatal(err)
	}
	if len(items[0].Presentations) != 1 {
		t.Fatalf("la presentación debe seguir expuesta para poder seleccionarla, hay %d", len(items[0].Presentations))
	}
	if items[0].Presentations[0].Stock != 0 {
		t.Fatalf("con show_stock=false, el stock de la presentación debe venir en 0, vino %.2f", items[0].Presentations[0].Stock)
	}
	if items[0].Presentations[0].Name != "Única" || items[0].Presentations[0].SalePrice != 20 {
		t.Fatalf("identidad de la presentación no debe alterarse por show_stock: %+v", items[0].Presentations[0])
	}
}

// TestPublicProducts_TenantIsolation: dos "tenants" (bases de datos sqlite independientes, mismo
// patrón que usa el resto del backend para aislar tenants — una BD por tenant) no deben filtrarse
// presentaciones entre sí. Cada EcommerceService opera exclusivamente sobre su propia conexión.
func TestPublicProducts_TenantIsolation(t *testing.T) {
	dbA := setupPublicProductsTestDB(t)
	dbB := setupPublicProductsTestDB(t)

	branchA := database.TenantBranch{Name: "Sucursal A"}
	dbA.Create(&branchA)
	prodA := database.TenantProduct{Code: "TA", Name: "Producto tenant A", Type: "product", Unit: "NIU", SalePrice: 10, HasVariants: true, Active: true, ShowInDigitalCatalog: true}
	dbA.Create(&prodA)
	presA := database.TenantProductPresentation{ProductID: prodA.ID, Name: "Solo en A", SalePrice: 10, Active: true}
	dbA.Create(&presA)

	svcB := &EcommerceService{db: dbB}
	itemsB, _, err := svcB.PublicProducts("", 0, nil, nil, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsB) != 0 {
		t.Fatalf("tenant B no debe ver productos/presentaciones del tenant A, vio %d productos", len(itemsB))
	}
}
