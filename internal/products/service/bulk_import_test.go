package service

import (
	"testing"

	"tukifac/pkg/database"
)

func TestBulkImport_InitialStockRegistersKardex(t *testing.T) {
	db := setupProductServiceTestDB(t)
	if err := db.AutoMigrate(
		&database.TenantBranch{},
		&database.TenantProductStock{},
		&database.TenantStockMovement{},
		// Todo movimiento de kardex resuelve su tipo de operación en este catálogo.
		&database.TenantInventoryOperationType{},
	); err != nil {
		t.Fatal(err)
	}
	if err := database.SeedInventoryOperationTypes(db); err != nil {
		t.Fatal(err)
	}
	branch := database.TenantBranch{Name: "Local 1", Active: true, IsMain: true}
	if err := db.Create(&branch).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewProductService(db)
	res, err := svc.BulkImportRestaurant([]BulkImportItem{
		{
			RowNumber:       2,
			Name:            "Plato con stock",
			SalePrice:       10,
			ManageStock:     true,
			InitialStock:    50,
			PreparationArea: "cocina",
		},
	}, branch.ID, 1)
	if err != nil {
		t.Fatalf("BulkImportRestaurant: %v", err)
	}
	if res.Created != 1 {
		t.Fatalf("created: got %d, want 1", res.Created)
	}
	if res.StockRegistered != 1 {
		t.Fatalf("stock_registered: got %d, want 1", res.StockRegistered)
	}

	var stock database.TenantProductStock
	if err := db.Where("branch_id = ?", branch.ID).First(&stock).Error; err != nil {
		t.Fatalf("stock row: %v", err)
	}
	if stock.Quantity != 50 {
		t.Fatalf("quantity: got %v, want 50", stock.Quantity)
	}

	var movement database.TenantStockMovement
	if err := db.Where("branch_id = ? AND reference = ?", branch.ID, "STOCK_INICIAL").First(&movement).Error; err != nil {
		t.Fatalf("movement: %v", err)
	}
	if movement.Quantity != 50 || movement.Type != "in" {
		t.Fatalf("movement: type=%s qty=%v", movement.Type, movement.Quantity)
	}
	if movement.Balance != 50 {
		t.Fatalf("balance: got %v, want 50", movement.Balance)
	}
}

func TestBulkImport_InitialStockWithoutManageStockFailsRow(t *testing.T) {
	db := setupProductServiceTestDB(t)
	if err := db.AutoMigrate(&database.TenantBranch{}); err != nil {
		t.Fatal(err)
	}
	branch := database.TenantBranch{Name: "Local 1", Active: true, IsMain: true}
	if err := db.Create(&branch).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewProductService(db)
	res, err := svc.BulkImportRestaurant([]BulkImportItem{
		{
			RowNumber:    2,
			Name:         "Plato test",
			SalePrice:    10,
			ManageStock:  false,
			InitialStock: 12,
		},
	}, branch.ID, 1)
	if err != nil {
		t.Fatalf("BulkImportRestaurant: %v", err)
	}
	if res.Created != 0 {
		t.Fatalf("created: got %d, want 0", res.Created)
	}
	if len(res.Failed) != 1 {
		t.Fatalf("failed count: got %d, want 1", len(res.Failed))
	}
	if res.Failed[0].Error != InitialStockRequiresManageStock {
		t.Fatalf("error: got %q", res.Failed[0].Error)
	}
}

func TestBulkImport_CatalogManageStockCreatesBranchLinkAtZero(t *testing.T) {
	db := setupProductServiceTestDB(t)
	if err := db.AutoMigrate(
		&database.TenantBranch{},
		&database.TenantProductStock{},
	); err != nil {
		t.Fatal(err)
	}
	branch := database.TenantBranch{Name: "Local 1", Active: true, IsMain: true}
	if err := db.Create(&branch).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewProductService(db)
	res, err := svc.BulkImportCatalog([]BulkImportItem{
		{
			RowNumber:   2,
			Name:        "Producto catálogo",
			Code:        "CAT-001",
			SalePrice:   10,
			ManageStock: true,
		},
	}, branch.ID, 1)
	if err != nil {
		t.Fatalf("BulkImportCatalog: %v", err)
	}
	if res.Created != 1 {
		t.Fatalf("created: got %d, want 1", res.Created)
	}

	var stock database.TenantProductStock
	if err := db.Where("branch_id = ?", branch.ID).First(&stock).Error; err != nil {
		t.Fatalf("stock row: %v", err)
	}
	if stock.Quantity != 0 {
		t.Fatalf("quantity: got %v, want 0", stock.Quantity)
	}
}

// TestBulkImportCatalog_BrandNameFindOrCreate cubre la columna opcional "marca" de la plantilla
// de Excel: crea la marca si no existe, reutiliza la existente (case-insensitive) sin duplicar,
// y no borra la marca de un producto existente si la fila no trae el dato (igual que category_name).
func TestBulkImportCatalog_BrandNameFindOrCreate(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)

	res, err := svc.BulkImportCatalog([]BulkImportItem{
		{RowNumber: 2, Name: "Zapatilla A", Code: "BR-001", SalePrice: 10, BrandName: "Nike"},
		{RowNumber: 3, Name: "Zapatilla B", Code: "BR-002", SalePrice: 12, BrandName: "nike"}, // mismo nombre, otra capitalización
		{RowNumber: 4, Name: "Polo sin marca", Code: "BR-003", SalePrice: 8},
	}, 0, 1)
	if err != nil {
		t.Fatalf("BulkImportCatalog: %v", err)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("failed: %+v", res.Failed)
	}
	if res.Created != 3 {
		t.Fatalf("created: got %d, want 3", res.Created)
	}

	var brands []database.TenantBrand
	if err := db.Find(&brands).Error; err != nil {
		t.Fatal(err)
	}
	if len(brands) != 1 {
		t.Fatalf("marcas creadas: got %d, want 1 (no debe duplicar por mayúsculas)", len(brands))
	}
	if brands[0].Name != "Nike" {
		t.Fatalf("nombre de marca: got %q, want %q (conserva la primera capitalización)", brands[0].Name, "Nike")
	}

	var p1, p2, p3 database.TenantProduct
	db.Where("code = ?", "BR-001").First(&p1)
	db.Where("code = ?", "BR-002").First(&p2)
	db.Where("code = ?", "BR-003").First(&p3)
	if p1.BrandID == nil || *p1.BrandID != brands[0].ID {
		t.Fatalf("producto BR-001 no vinculado a la marca: %+v", p1.BrandID)
	}
	if p2.BrandID == nil || *p2.BrandID != brands[0].ID {
		t.Fatalf("producto BR-002 no vinculado a la misma marca (case-insensitive): %+v", p2.BrandID)
	}
	if p3.BrandID != nil {
		t.Fatalf("producto BR-003 no debía tener marca: %+v", p3.BrandID)
	}

	// Actualizar BR-001 sin mandar brand_name no debe borrar la marca ya asignada.
	res2, err := svc.BulkImportCatalog([]BulkImportItem{
		{RowNumber: 2, Name: "Zapatilla A", Code: "BR-001", SalePrice: 15},
	}, 0, 1)
	if err != nil {
		t.Fatalf("BulkImportCatalog (update): %v", err)
	}
	if res2.Updated != 1 {
		t.Fatalf("updated: got %d, want 1", res2.Updated)
	}
	db.Where("code = ?", "BR-001").First(&p1)
	if p1.BrandID == nil || *p1.BrandID != brands[0].ID {
		t.Fatalf("marca no debía perderse al actualizar sin brand_name: %+v", p1.BrandID)
	}
}
