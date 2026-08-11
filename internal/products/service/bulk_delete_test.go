package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupBulkDeleteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantProduct{},
		&database.TenantProductPresentation{},
		&database.TenantProductModifierGroup{},
		&database.TenantProductStock{},
		&database.TenantStockMovement{},
		&database.TenantSaleItem{},
		&database.TenantPurchaseItem{},
		&database.TenantComanda{},
		&database.TenantTransferLog{},
		&database.TenantProductSerial{},
		&database.TenantMembership{},
		&database.TenantRestaurantSetting{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantRestaurantSetting{DeletionPin: "1234"}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func createRestaurantProduct(t *testing.T, db *gorm.DB, code, name string) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{
		Code: code, Name: name, ManageStock: false, IsRestaurant: true,
		BranchID: 1, Active: true, Type: "product", Unit: "NIU", SalePrice: 10,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func createCatalogProduct(t *testing.T, db *gorm.DB, code, name string) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{
		Code: code, Name: name, ManageStock: false, IsRestaurant: false,
		Active: true, Type: "product", Unit: "NIU", SalePrice: 10,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBulkDeleteCatalog_DeletesCleanProduct(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	p := createCatalogProduct(t, db, "CAT-1", "Catálogo limpio")

	svc := NewProductService(db)
	res, err := svc.BulkDeleteCatalog(BulkDeleteCatalogInput{
		ProductIDs: []uint{p.ID}, Reason: "depuración", UserID: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0].ID != p.ID {
		t.Fatalf("deleted=%+v", res.Deleted)
	}
	var prodCount int64
	db.Unscoped().Model(&database.TenantProduct{}).Where("id = ?", p.ID).Count(&prodCount)
	if prodCount != 0 {
		t.Fatalf("product still exists")
	}
}

func TestBulkDeleteCatalog_WorksWithoutRestaurantPin(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	if err := db.Exec("DELETE FROM tenant_restaurant_settings").Error; err != nil {
		t.Fatal(err)
	}
	p := createCatalogProduct(t, db, "CAT-2", "Sin PIN restaurante")

	svc := NewProductService(db)
	res, err := svc.BulkDeleteCatalog(BulkDeleteCatalogInput{
		ProductIDs: []uint{p.ID}, Reason: "depuración", UserID: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 {
		t.Fatalf("deleted=%+v", res.Deleted)
	}
}

func TestBulkDeleteRestaurant_InvalidPinDeletesNothing(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	p := createRestaurantProduct(t, db, "OK-1", "Plato limpio")

	svc := NewProductService(db)
	res, err := svc.BulkDeleteRestaurant(BulkDeleteRestaurantInput{
		ProductIDs: []uint{p.ID},
		Pin:        "9999",
		Reason:     "test",
		UserID:     1,
	})
	if err == nil {
		t.Fatal("expected PIN error")
	}
	var pinErr *PinVerificationError
	if !asPinError(err, &pinErr) {
		t.Fatalf("expected PinVerificationError, got %T: %v", err, err)
	}
	if res != nil {
		t.Fatal("expected nil result on PIN error")
	}
	var count int64
	db.Model(&database.TenantProduct{}).Where("id = ?", p.ID).Count(&count)
	if count != 1 {
		t.Fatalf("product should remain, count=%d", count)
	}
}

func asPinError(err error, target **PinVerificationError) bool {
	if err == nil {
		return false
	}
	if pe, ok := err.(*PinVerificationError); ok {
		*target = pe
		return true
	}
	return false
}

func TestBulkDeleteRestaurant_BlockedBySales(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	p := createRestaurantProduct(t, db, "SALE-1", "Con venta")
	pid := p.ID
	db.Create(&database.TenantSaleItem{SaleID: 1, ProductID: &pid, Description: "x", Quantity: 1, UnitPrice: 10, Subtotal: 10, TaxAmount: 0, Total: 10})

	svc := NewProductService(db)
	res, err := svc.BulkDeleteRestaurant(BulkDeleteRestaurantInput{
		ProductIDs: []uint{p.ID}, Pin: "1234", Reason: "test", UserID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 || len(res.Blocked) != 1 {
		t.Fatalf("deleted=%d blocked=%d", len(res.Deleted), len(res.Blocked))
	}
	if res.Blocked[0].Reasons[0] != blockReasonHasSales {
		t.Fatalf("reason=%v", res.Blocked[0].Reasons)
	}
}

func TestBulkDeleteRestaurant_DeletesDespiteKardex(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	p := createRestaurantProduct(t, db, "KDX-1", "Con kardex")
	db.Create(&database.TenantStockMovement{
		ProductID: p.ID, BranchID: 1, Type: "in", Quantity: 5, Balance: 5, Reference: "STOCK_INICIAL",
	})

	svc := NewProductService(db)
	res, err := svc.BulkDeleteRestaurant(BulkDeleteRestaurantInput{
		ProductIDs: []uint{p.ID}, Pin: "1234", Reason: "test", UserID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 {
		t.Fatalf("expected delete despite kardex, deleted=%d blocked=%+v", len(res.Deleted), res.Blocked)
	}
}

func TestBulkDeleteRestaurant_DeletesDespiteStock(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	p := createRestaurantProduct(t, db, "STK-1", "Con stock")
	db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 3})

	svc := NewProductService(db)
	res, err := svc.BulkDeleteRestaurant(BulkDeleteRestaurantInput{
		ProductIDs: []uint{p.ID}, Pin: "1234", Reason: "test", UserID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 {
		t.Fatalf("expected delete despite stock, deleted=%d blocked=%+v", len(res.Deleted), res.Blocked)
	}
}

func TestBulkDeleteRestaurant_PhysicalDeleteCleanProduct(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	p := createRestaurantProduct(t, db, "CLN-1", "Limpio")

	svc := NewProductService(db)
	res, err := svc.BulkDeleteRestaurant(BulkDeleteRestaurantInput{
		ProductIDs: []uint{p.ID}, Pin: "1234", Reason: "depuración", UserID: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0].ID != p.ID {
		t.Fatalf("deleted=%+v", res.Deleted)
	}
	var prodCount int64
	db.Unscoped().Model(&database.TenantProduct{}).Where("id = ?", p.ID).Count(&prodCount)
	if prodCount != 0 {
		t.Fatalf("tenant_products: got %d rows, want 0 (physical delete)", prodCount)
	}
}

func TestBulkDeleteRestaurant_PhysicalDeletePresentations(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	p := createRestaurantProduct(t, db, "CLN-PRES", "Con presentación")
	db.Create(&database.TenantProductPresentation{ProductID: p.ID, Name: "Grande", SalePrice: 12, Active: true})
	db.Create(&database.TenantProductModifierGroup{ProductID: p.ID, GroupID: 1})

	svc := NewProductService(db)
	res, err := svc.BulkDeleteRestaurant(BulkDeleteRestaurantInput{
		ProductIDs: []uint{p.ID}, Pin: "1234", Reason: "depuración", UserID: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 {
		t.Fatalf("deleted=%d want 1", len(res.Deleted))
	}
	var presCount int64
	db.Unscoped().Model(&database.TenantProductPresentation{}).Where("product_id = ?", p.ID).Count(&presCount)
	if presCount != 0 {
		t.Fatalf("tenant_product_presentations: got %d rows, want 0 (physical delete)", presCount)
	}
	var prodCount int64
	db.Unscoped().Model(&database.TenantProduct{}).Where("id = ?", p.ID).Count(&prodCount)
	if prodCount != 0 {
		t.Fatalf("tenant_products: got %d rows, want 0", prodCount)
	}
}

func TestBulkDeleteRestaurant_ReuseCodeAfterPhysicalDelete(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	const code = "EAN-990011"
	p := createRestaurantProduct(t, db, code, "Plato original")

	svc := NewProductService(db)
	res, err := svc.BulkDeleteRestaurant(BulkDeleteRestaurantInput{
		ProductIDs: []uint{p.ID}, Pin: "1234", Reason: "depuración", UserID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 {
		t.Fatalf("deleted=%d want 1", len(res.Deleted))
	}

	var totalWithCode int64
	db.Unscoped().Model(&database.TenantProduct{}).Where("code = ? AND branch_id = ?", code, 1).Count(&totalWithCode)
	if totalWithCode != 0 {
		t.Fatalf("Unscoped rows with code %q: got %d want 0 before recreate", code, totalWithCode)
	}

	created, _, err := svc.Create(ProductInput{
		Code:         code,
		Name:         "Plato reutilizado",
		Type:         "product",
		Unit:         "NIU",
		SalePrice:    15,
		TaxRate:      18,
		IgvAffectationType: "10",
		IsRestaurant: true,
		BranchID:     1,
		Active:       true,
	})
	if err != nil {
		t.Fatalf("Create with reused code: %v", err)
	}
	if created.Code != code {
		t.Fatalf("code=%q want %q", created.Code, code)
	}

	var activeCount int64
	db.Model(&database.TenantProduct{}).Where("code = ? AND branch_id = ?", code, 1).Count(&activeCount)
	if activeCount != 1 {
		t.Fatalf("active products with code %q: got %d want 1", code, activeCount)
	}
	found, err := svc.GetByCodeInBranch(code, 1)
	if err != nil {
		t.Fatal(err)
	}
	if found == nil || found.ID != created.ID {
		t.Fatalf("GetByCodeInBranch: got %+v want id=%d", found, created.ID)
	}
}

func TestBulkDeleteRestaurant_DeletesCleanProduct(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	p := createRestaurantProduct(t, db, "CLN-1", "Limpio")
	db.Create(&database.TenantProductPresentation{ProductID: p.ID, Name: "Grande", SalePrice: 12, Active: true})
	db.Create(&database.TenantProductModifierGroup{ProductID: p.ID, GroupID: 1})

	svc := NewProductService(db)
	res, err := svc.BulkDeleteRestaurant(BulkDeleteRestaurantInput{
		ProductIDs: []uint{p.ID}, Pin: "1234", Reason: "depuración", UserID: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0].ID != p.ID {
		t.Fatalf("deleted=%+v", res.Deleted)
	}
	var prodCount, presCount, modCount int64
	db.Unscoped().Model(&database.TenantProduct{}).Where("id = ?", p.ID).Count(&prodCount)
	db.Unscoped().Model(&database.TenantProductPresentation{}).Where("product_id = ?", p.ID).Count(&presCount)
	db.Model(&database.TenantProductModifierGroup{}).Where("product_id = ?", p.ID).Count(&modCount)
	if prodCount != 0 || presCount != 0 || modCount != 0 {
		t.Fatalf("remnants prod=%d pres=%d mod=%d", prodCount, presCount, modCount)
	}
}

func TestBulkDeleteRestaurant_MixedBatchPartialResult(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	svc := NewProductService(db)

	var validIDs []uint
	for i := 1; i <= 5; i++ {
		p := createRestaurantProduct(t, db, fmt.Sprintf("OK-%d", i), fmt.Sprintf("Válido %d", i))
		validIDs = append(validIDs, p.ID)
	}

	blocked := make([]uint, 0, 2)
	for i := 1; i <= 2; i++ {
		p := createRestaurantProduct(t, db, fmt.Sprintf("BLK-%d", i), fmt.Sprintf("Bloqueado %d", i))
		blocked = append(blocked, p.ID)
		switch i {
		case 1:
			pid := p.ID
			db.Create(&database.TenantSaleItem{SaleID: 1, ProductID: &pid, Description: "x", Quantity: 1, UnitPrice: 1, Subtotal: 1, TaxAmount: 0, Total: 1})
		case 2:
			pid := p.ID
			db.Create(&database.TenantPurchaseItem{PurchaseID: 1, ProductID: &pid, Description: "x", Quantity: 1, UnitCost: 1, Subtotal: 1, TaxAmount: 0, Total: 1})
		}
	}

	// Productos con kardex/stock/comandas deben poder eliminarse físicamente.
	for i := 1; i <= 3; i++ {
		p := createRestaurantProduct(t, db, fmt.Sprintf("HIST-%d", i), fmt.Sprintf("Historial %d", i))
		validIDs = append(validIDs, p.ID)
		switch i {
		case 1:
			db.Create(&database.TenantStockMovement{ProductID: p.ID, BranchID: 1, Type: "in", Quantity: 1, Balance: 1})
		case 2:
			db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 2})
		case 3:
			pid := p.ID
			db.Create(&database.TenantComanda{OrderID: 1, SessionID: 1, ProductID: &pid, ProductName: "x", Quantity: 1, UnitPrice: 1})
		}
	}

	allIDs := append(append([]uint{}, validIDs...), blocked...)
	res, err := svc.BulkDeleteRestaurant(BulkDeleteRestaurantInput{
		ProductIDs: allIDs, Pin: "1234", Reason: "lote mixto", UserID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 8 {
		t.Fatalf("deleted=%d want 8", len(res.Deleted))
	}
	if len(res.Blocked) != 2 {
		t.Fatalf("blocked=%d want 2", len(res.Blocked))
	}
	for _, id := range validIDs {
		var n int64
		db.Unscoped().Model(&database.TenantProduct{}).Where("id = ?", id).Count(&n)
		if n != 0 {
			t.Fatalf("valid id %d should be physically deleted (Unscoped count=%d)", id, n)
		}
	}
	for _, id := range blocked {
		var n int64
		db.Model(&database.TenantProduct{}).Where("id = ?", id).Count(&n)
		if n != 1 {
			t.Fatalf("blocked id %d should remain", id)
		}
	}
}

func TestCreate_RestoresSoftDeletedByCode(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	p := createCatalogProduct(t, db, "REST-001", "Oculto")
	svc := NewProductService(db)
	if err := svc.Delete(p.ID); err != nil {
		t.Fatal(err)
	}

	restored, _, err := svc.Create(ProductInput{
		Code:               "REST-001",
		Name:               "Reactivado",
		Type:               "product",
		Unit:               "NIU",
		SalePrice:          25,
		TaxRate:            18,
		IgvAffectationType: "10",
		Active:             true,
	})
	if err != nil {
		t.Fatalf("Create restore: %v", err)
	}
	if restored.ID != p.ID {
		t.Fatalf("id=%d want %d (same row restored)", restored.ID, p.ID)
	}
	if restored.Name != "Reactivado" {
		t.Fatalf("name=%q", restored.Name)
	}
}

func TestBulkImportCatalog_RestoresSoftDeletedByCode(t *testing.T) {
	db := setupBulkDeleteTestDB(t)
	p := createCatalogProduct(t, db, "IMP-777", "Import oculto")
	svc := NewProductService(db)
	if err := svc.Delete(p.ID); err != nil {
		t.Fatal(err)
	}

	res, err := svc.BulkImportCatalog([]BulkImportItem{{
		RowNumber: 2,
		Name:      "Reimportado",
		Code:      "IMP-777",
		SalePrice: 19.9,
		Unit:      "NIU",
	}}, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 || res.Created != 0 {
		t.Fatalf("updated=%d created=%d", res.Updated, res.Created)
	}
	got, err := svc.GetByID(p.ID)
	if err != nil || got == nil {
		t.Fatal("product should be visible again")
	}
	if got.Name != "Reimportado" {
		t.Fatalf("name=%q", got.Name)
	}
}
