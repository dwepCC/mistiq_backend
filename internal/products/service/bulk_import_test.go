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
			RowNumber:    2,
			Name:         "Plato con stock",
			SalePrice:    10,
			ManageStock:  true,
			InitialStock: 50,
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
			RowNumber:   2,
			Name:        "Plato test",
			SalePrice:   10,
			ManageStock: false,
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
