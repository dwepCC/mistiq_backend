package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupV083TestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	branch := database.TenantBranch{Name: "Principal", Active: true, IsMain: true}
	if err := db.Create(&branch).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestV083InventoryIngressEgress_SeedIdempotent(t *testing.T) {
	db := setupV083TestDB(t)
	mig := V083InventoryIngressEgress{}

	if err := mig.Up(db); err != nil {
		t.Fatalf("first Up: %v", err)
	}

	var opCount int64
	if err := db.Model(&database.TenantInventoryOperationType{}).Count(&opCount).Error; err != nil {
		t.Fatal(err)
	}
	if opCount != 20 {
		t.Fatalf("operation types = %d, want 20", opCount)
	}

	var seriesCount int64
	if err := db.Model(&database.TenantDocumentSeries{}).
		Where("category = ?", "almacen").
		Count(&seriesCount).Error; err != nil {
		t.Fatal(err)
	}
	if seriesCount != 2 {
		t.Fatalf("almacen series = %d, want 2 (ING001+EGR001)", seriesCount)
	}

	var mov database.TenantStockMovement
	if !db.Migrator().HasColumn(&mov, "OperationTypeID") {
		t.Fatal("expected OperationTypeID column on tenant_stock_movements")
	}
	if !db.Migrator().HasColumn(&mov, "InventoryDocumentID") {
		t.Fatal("expected InventoryDocumentID column on tenant_stock_movements")
	}

	if err := mig.Up(db); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	var opCount2 int64
	db.Model(&database.TenantInventoryOperationType{}).Count(&opCount2)
	if opCount2 != 20 {
		t.Fatalf("after re-run operation types = %d, want 20", opCount2)
	}
}

func TestSeedInventoryOperationTypes_AllCodesPresent(t *testing.T) {
	db := setupV083TestDB(t)
	if err := (V083InventoryIngressEgress{}).Up(db); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"PURCHASE", "INITIAL_STOCK", "RETURN_IN", "CONSIGNMENT_IN", "OTHER_IN", "INVENTORY_ADJUSTMENT_IN", "TRANSFER",
		"SALE", "RETURN_OUT", "DONATION", "PRODUCTION_OUT", "WITHDRAWAL",
		"SHRINKAGE", "WASTE", "DESTRUCTION", "CONSIGNMENT_OUT", "PROMOTION", "PRIZE", "OTHER_OUT", "INVENTORY_ADJUSTMENT_OUT",
	}
	for _, code := range want {
		var n int64
		db.Model(&database.TenantInventoryOperationType{}).Where("code = ?", code).Count(&n)
		if n != 1 {
			t.Fatalf("missing operation type %s", code)
		}
	}
}
