package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupProductServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantProduct{}, &database.TenantCategory{}, &database.TenantPreparationArea{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestProductCreate_ManageStockFalsePersistsInDB(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)

	p, _, err := svc.Create(ProductInput{
		Code:               "TST-NO-STOCK",
		Name:               "Sin control stock",
		Type:               "product",
		Unit:               "NIU",
		SalePrice:          10,
		TaxRate:            18,
		IgvAffectationType: "10",
		ManageStock:        false,
		Active:             true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ManageStock {
		t.Fatalf("ManageStock en memoria: got true, want false")
	}

	var loaded database.TenantProduct
	if err := db.First(&loaded, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.ManageStock {
		t.Fatalf("manage_stock en BD: got true, want false")
	}
}

func TestProductList_NoManageStockOnly(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	branchID := uint(1)
	_, _, err := svc.Create(ProductInput{
		Code: "WITH-STK", Name: "Con stock", Type: "product", Unit: "NIU",
		SalePrice: 10, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: true, IsRestaurant: true, BranchID: branchID, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.Create(ProductInput{
		Code: "NO-STK", Name: "Sin stock", Type: "product", Unit: "NIU",
		SalePrice: 10, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: false, IsRestaurant: true, BranchID: branchID, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	items, total, err := svc.ListReport(ProductListParams{
		RestaurantOnly:    true,
		NoManageStockOnly: true,
		ActiveOnly:        true,
		BranchID:          branchID,
		Limit:             50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("total=%d len=%d want 1", total, len(items))
	}
	if items[0].Code != "NO-STK" {
		t.Fatalf("got %q want NO-STK", items[0].Code)
	}
}

func TestProductCreate_DefaultManageStockFalseWhenOmitted(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)

	p, _, err := svc.Create(ProductInput{
		Code:               "TST-DEFAULT-NO-STOCK",
		Name:               "Producto sin flag explícito",
		Type:               "product",
		Unit:               "NIU",
		SalePrice:          12,
		TaxRate:            18,
		IgvAffectationType: "10",
		Active:             true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ManageStock {
		t.Fatalf("ManageStock en memoria: got true, want false (default)")
	}

	var loaded database.TenantProduct
	if err := db.First(&loaded, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.ManageStock {
		t.Fatalf("manage_stock en BD: got true, want false (default)")
	}
}

func TestProductCreate_ManageStockTruePersistsInDB(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)

	p, _, err := svc.Create(ProductInput{
		Code:               "TST-WITH-STOCK",
		Name:               "Con control stock",
		Type:               "product",
		Unit:               "NIU",
		SalePrice:          15,
		TaxRate:            18,
		IgvAffectationType: "10",
		ManageStock:        true,
		Active:             true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !p.ManageStock {
		t.Fatalf("ManageStock en memoria: got false, want true")
	}

	var loaded database.TenantProduct
	if err := db.First(&loaded, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !loaded.ManageStock {
		t.Fatalf("manage_stock en BD: got false, want true")
	}
}

func TestProductCreate_NonRestaurantClearsPreparationArea(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)

	p, _, err := svc.Create(ProductInput{
		Code: "ERP-1", Name: "Producto ERP", Type: "product", Unit: "NIU",
		SalePrice: 10, TaxRate: 18, IgvAffectationType: "10",
		IsRestaurant: false, PreparationArea: "cocina", Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.PreparationArea != "" {
		t.Fatalf("PreparationArea=%q want empty", p.PreparationArea)
	}
}

func TestProductCreate_ManageStockFalseClearsMinStock(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)

	p, _, err := svc.Create(ProductInput{
		Code: "NO-MIN", Name: "Sin min", Type: "product", Unit: "NIU",
		SalePrice: 10, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: false, MinStock: 5, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.MinStock != 0 {
		t.Fatalf("MinStock=%v want 0", p.MinStock)
	}
}

func TestProductUpdate_DemoteRestaurantClearsPreparationArea(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)

	p, _, err := svc.Create(ProductInput{
		Code: "REST-1", Name: "Plato", Type: "product", Unit: "NIU",
		SalePrice: 10, TaxRate: 18, IgvAffectationType: "10",
		IsRestaurant: true, PreparationArea: "bar", BranchID: 1, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(p.ID, ProductInput{
		Code: p.Code, Name: p.Name, Type: "product", Unit: "NIU",
		SalePrice: 10, TaxRate: 18, IgvAffectationType: "10",
		IsRestaurant: false, PreparationArea: "bar", ManageStock: true,
	}); err != nil {
		t.Fatal(err)
	}
	var loaded database.TenantProduct
	if err := db.First(&loaded, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.PreparationArea != "" {
		t.Fatalf("PreparationArea=%q want empty after demote", loaded.PreparationArea)
	}
}

func TestProductList_DefaultSortByIDDesc(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	branchID := uint(1)
	for i, name := range []string{"Primero", "Segundo", "Tercero"} {
		_, _, err := svc.Create(ProductInput{
			Code: fmt.Sprintf("P%d", i+1), Name: name, Type: "product", Unit: "NIU",
			SalePrice: float64(i+1) * 10, TaxRate: 18, IgvAffectationType: "10",
			IsRestaurant: true, BranchID: branchID, Active: true,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	items, _, err := svc.ListWithCategoryNames(ProductListParams{
		RestaurantOnly: true,
		ActiveOnly:     true,
		BranchID:       branchID,
		Limit:          10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 3 {
		t.Fatalf("len=%d want >=3", len(items))
	}
	if items[0].Name != "Tercero" || items[len(items)-1].Name != "Primero" {
		t.Fatalf("default order got %q..%q want Tercero..Primero", items[0].Name, items[len(items)-1].Name)
	}
}

func TestProductList_SortByNameAsc(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	branchID := uint(1)
	for _, name := range []string{"Zeta", "Alpha"} {
		_, _, err := svc.Create(ProductInput{
			Code: name, Name: name, Type: "product", Unit: "NIU",
			SalePrice: 10, TaxRate: 18, IgvAffectationType: "10",
			IsRestaurant: true, BranchID: branchID, Active: true,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	items, _, err := svc.ListWithCategoryNames(ProductListParams{
		RestaurantOnly: true,
		ActiveOnly:     true,
		BranchID:       branchID,
		SortBy:         "name",
		SortDir:        "asc",
		Limit:          10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Name != "Alpha" || items[1].Name != "Zeta" {
		t.Fatalf("sort name asc: %#v", items)
	}
}

func TestCategoryCRUD_sortOrderAndDeleteGuard(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)

	order1 := 10
	c1, err := svc.CreateCategory("Bebidas", "", &order1)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := svc.CreateCategory("Platos", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c2.SortOrder <= c1.SortOrder {
		t.Fatalf("auto sort_order got %d want > %d", c2.SortOrder, c1.SortOrder)
	}

	cats, err := svc.ListCategories()
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 2 || cats[0].Name != "Bebidas" {
		t.Fatalf("list order: %#v", cats)
	}

	order5 := 5
	if _, err := svc.UpdateCategory(c2.ID, "Entradas", "desc", order5); err != nil {
		t.Fatal(err)
	}
	cats, _ = svc.ListCategories()
	if cats[0].Name != "Entradas" {
		t.Fatalf("after update order: %#v", cats)
	}

	if err := svc.DeleteCategory(c1.ID); err != nil {
		t.Fatal(err)
	}

	cid := c2.ID
	if _, _, err := svc.Create(ProductInput{
		Code: "P1", Name: "Prod", Type: "product", Unit: "NIU",
		SalePrice: 10, TaxRate: 18, IgvAffectationType: "10",
		CategoryID: &cid, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteCategory(c2.ID); err == nil {
		t.Fatal("expected delete blocked with linked product")
	}
}

func TestPreparationAreaCRUD_linksProductByID(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)

	area, err := svc.CreatePreparationArea("Cocina", "cocina", nil)
	if err != nil {
		t.Fatal(err)
	}
	aid := area.ID
	p, _, err := svc.Create(ProductInput{
		Code: "R1", Name: "Plato", Type: "product", Unit: "NIU",
		SalePrice: 10, TaxRate: 18, IgvAffectationType: "10",
		IsRestaurant: true, PreparationAreaID: &aid, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.PreparationArea != "cocina" || p.PreparationAreaID == nil || *p.PreparationAreaID != aid {
		t.Fatalf("product area: id=%v slug=%q", p.PreparationAreaID, p.PreparationArea)
	}

	if _, _, err := svc.Create(ProductInput{
		Code: "R2", Name: "Otro", Type: "product", Unit: "NIU",
		SalePrice: 10, TaxRate: 18, IgvAffectationType: "10",
		IsRestaurant: true, PreparationAreaID: &aid, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeletePreparationArea(aid); err == nil {
		t.Fatal("expected delete blocked with linked products")
	}
}
