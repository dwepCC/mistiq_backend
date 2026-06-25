package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupKardexTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantProduct{},
		&database.TenantBranch{},
		&database.TenantProductStock{},
		&database.TenantStockMovement{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestGetKardex_RestaurantOnlyFiltersProducts(t *testing.T) {
	db := setupKardexTestDB(t)
	branch := database.TenantBranch{Name: "Local", Active: true, IsMain: true}
	db.Create(&branch)

	restaurantStock := database.TenantProduct{
		Code: "R-STOCK", Name: "Plato stock", ManageStock: true, IsRestaurant: true,
		BranchID: branch.ID, Active: true, Type: "product", Unit: "NIU",
	}
	restaurantNoStock := database.TenantProduct{
		Code: "R-NOSTK", Name: "Plato sin stock", ManageStock: false, IsRestaurant: true,
		BranchID: branch.ID, Active: true, Type: "product", Unit: "NIU",
	}
	erpProduct := database.TenantProduct{
		Code: "ERP-1", Name: "Producto ERP", ManageStock: true, IsRestaurant: false,
		Active: true, Type: "product", Unit: "NIU",
	}
	db.Create(&restaurantStock)
	db.Create(&restaurantNoStock)
	db.Create(&erpProduct)

	movs := []database.TenantStockMovement{
		{ProductID: restaurantStock.ID, BranchID: branch.ID, Type: "in", Quantity: 10, Balance: 10, Reference: "STOCK_INICIAL"},
		{ProductID: erpProduct.ID, BranchID: branch.ID, Type: "in", Quantity: 3, Balance: 3, Reference: "STOCK_INICIAL"},
	}
	for _, m := range movs {
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc := NewInventoryService(db)
	list, total, err := svc.GetKardex(KardexParams{RestaurantOnly: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetKardex: %v", err)
	}
	if total != 1 {
		t.Fatalf("total: got %d, want 1", total)
	}
	if len(list) != 1 || list[0].ProductID != restaurantStock.ID {
		t.Fatalf("expected only restaurant manage_stock movement, got %d rows product_id=%d", len(list), list[0].ProductID)
	}
}

func TestRecordAdjustment_InAndOut(t *testing.T) {
	db := setupKardexTestDB(t)
	branch := database.TenantBranch{Name: "Local", Active: true, IsMain: true}
	db.Create(&branch)
	p := database.TenantProduct{
		Code: "ADJ-1", Name: "Plato", ManageStock: true, IsRestaurant: true,
		BranchID: branch.ID, Active: true, Type: "product", Unit: "NIU",
	}
	db.Create(&p)
	db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: branch.ID, Quantity: 100})

	svc := NewInventoryService(db)
	if err := svc.RecordAdjustment(AdjustmentInput{
		ProductID: p.ID, BranchID: branch.ID, Type: "in", Quantity: 50, Notes: "entrada test",
	}, 1); err != nil {
		t.Fatalf("adjustment in: %v", err)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, branch.ID).First(&stock)
	if stock.Quantity != 150 {
		t.Fatalf("after +50: quantity=%v want 150", stock.Quantity)
	}

	if err := svc.RecordAdjustment(AdjustmentInput{
		ProductID: p.ID, BranchID: branch.ID, Type: "out", Quantity: 20, Notes: "salida test",
	}, 1); err != nil {
		t.Fatalf("adjustment out: %v", err)
	}
	db.Where("product_id = ? AND branch_id = ?", p.ID, branch.ID).First(&stock)
	if stock.Quantity != 130 {
		t.Fatalf("after -20: quantity=%v want 130", stock.Quantity)
	}

	var adjIn, adjOut int64
	db.Model(&database.TenantStockMovement{}).Where("product_id = ? AND type = ?", p.ID, "adjustment_in").Count(&adjIn)
	db.Model(&database.TenantStockMovement{}).Where("product_id = ? AND type = ? AND reference = ?", p.ID, "adjustment_out", "AJUSTE").Count(&adjOut)
	if adjIn != 1 || adjOut != 1 {
		t.Fatalf("kardex adjustments: in=%d out=%d", adjIn, adjOut)
	}
}
