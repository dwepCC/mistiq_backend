package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantmigrations"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupDocumentTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	branch := database.TenantBranch{Name: "Local", Active: true, IsMain: true}
	if err := db.Create(&branch).Error; err != nil {
		t.Fatal(err)
	}
	if err := (tenantmigrations.V083InventoryIngressEgress{}).Up(db); err != nil {
		t.Fatal(err)
	}
	if err := (tenantmigrations.V084InventoryOperationTransfer{}).Up(db); err != nil {
		t.Fatal(err)
	}
	if err := (tenantmigrations.V085InventoryDocumentSource{}).Up(db); err != nil {
		t.Fatal(err)
	}
	if err := database.SeedInventoryDocumentSeriesForBranch(db, branch.ID); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedProduct(t *testing.T, db *gorm.DB, branchID uint) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{
		Code: "P1", Name: "Producto test", ManageStock: true,
		BranchID: branchID, Active: true, Type: "product", Unit: "NIU",
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func otherInTypeID(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	var op database.TenantInventoryOperationType
	if err := db.Where("code = ?", "OTHER_IN").First(&op).Error; err != nil {
		t.Fatal(err)
	}
	return op.ID
}

func shrinkageTypeID(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	var op database.TenantInventoryOperationType
	if err := db.Where("code = ?", "SHRINKAGE").First(&op).Error; err != nil {
		t.Fatal(err)
	}
	return op.ID
}

func TestCreateConfirmVoidDocument(t *testing.T) {
	db := setupDocumentTestDB(t)
	var branch database.TenantBranch
	db.First(&branch)
	p := seedProduct(t, db, branch.ID)
	opID := shrinkageTypeID(t, db)

	docSvc := NewInventoryDocumentService(db)
	docID, err := docSvc.CreateInventoryDocument(CreateDocumentInput{
		Direction: "OUT", OperationTypeID: opID, BranchID: branch.ID,
		DocumentDate: time.Now(), MovementReason: "Merma prueba",
		Lines:        []DocumentLineInput{{ProductID: p.ID, Quantity: 5}},
		UserID:       1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: branch.ID, Quantity: 10})
	if err := docSvc.ConfirmInventoryDocument(docID, 1); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	var doc database.TenantInventoryDocument
	db.First(&doc, docID)
	if doc.Status != DocumentStatusConfirmed {
		t.Fatalf("status = %s", doc.Status)
	}
	if doc.ConfirmedBy == nil || *doc.ConfirmedBy != 1 {
		t.Fatal("confirmed_by not set")
	}
	if doc.Number == "" || doc.Number == fmt.Sprintf("DRAFT-%d", docID) {
		t.Fatalf("expected numbered doc, got %s", doc.Number)
	}

	var movCount int64
	db.Model(&database.TenantStockMovement{}).Where("inventory_document_id = ?", docID).Count(&movCount)
	if movCount != 1 {
		t.Fatalf("movements = %d", movCount)
	}

	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, branch.ID).First(&stock)
	if stock.Quantity != 5 {
		t.Fatalf("stock = %v want 5", stock.Quantity)
	}

	if err := docSvc.ConfirmInventoryDocument(docID, 1); err != ErrDocumentAlreadyConfirmed {
		t.Fatalf("double confirm: %v", err)
	}

	if err := docSvc.VoidInventoryDocument(docID, 2); err != nil {
		t.Fatalf("void: %v", err)
	}
	db.First(&doc, docID)
	if doc.Status != DocumentStatusVoided || doc.VoidedBy == nil || *doc.VoidedBy != 2 {
		t.Fatal("void audit fields missing")
	}
	db.Where("product_id = ? AND branch_id = ?", p.ID, branch.ID).First(&stock)
	if stock.Quantity != 10 {
		t.Fatalf("stock after void = %v want 10", stock.Quantity)
	}
	db.Model(&database.TenantStockMovement{}).Where("inventory_document_id = ?", docID).Count(&movCount)
	if movCount != 2 {
		t.Fatalf("movements after void = %d want 2", movCount)
	}

	if err := docSvc.VoidInventoryDocument(docID, 2); err != ErrDocumentAlreadyVoided {
		t.Fatalf("double void: %v", err)
	}
}

func TestConfirmDirectionMismatch(t *testing.T) {
	db := setupDocumentTestDB(t)
	var branch database.TenantBranch
	db.First(&branch)
	p := seedProduct(t, db, branch.ID)

	var op database.TenantInventoryOperationType
	db.Where("code = ?", "DONATION").First(&op)

	docSvc := NewInventoryDocumentService(db)
	_, err := docSvc.CreateInventoryDocument(CreateDocumentInput{
		Direction: "IN", OperationTypeID: op.ID, BranchID: branch.ID,
		Lines:  []DocumentLineInput{{ProductID: p.ID, Quantity: 1, UnitCost: 1}},
		UserID: 1,
	})
	if err != ErrOperationDirectionMismatch {
		t.Fatalf("create mismatch: %v", err)
	}
}

func TestRecordAdjustmentViaDocument(t *testing.T) {
	db := setupDocumentTestDB(t)
	var branch database.TenantBranch
	db.First(&branch)
	p := seedProduct(t, db, branch.ID)

	inv := NewInventoryService(db)
	if err := inv.RecordAdjustment(AdjustmentInput{
		ProductID: p.ID, BranchID: branch.ID, Type: "in", Quantity: 3, Notes: "Ajuste test",
	}, 1); err != nil {
		t.Fatal(err)
	}

	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, branch.ID).First(&stock)
	if stock.Quantity != 3 {
		t.Fatalf("stock = %v", stock.Quantity)
	}

	var mov database.TenantStockMovement
	if err := db.Where("product_id = ? AND type = ?", p.ID, "in").First(&mov).Error; err != nil {
		t.Fatal(err)
	}
	if mov.InventoryDocumentID == nil {
		t.Fatal("expected inventory_document_id on movement")
	}
	if mov.OperationTypeID == nil {
		t.Fatal("expected operation_type_id")
	}
	op, _ := LookupOperationTypeByCode(db, "INVENTORY_ADJUSTMENT_IN")
	if *mov.OperationTypeID != op.ID {
		t.Fatalf("operation type = %d want %d", *mov.OperationTypeID, op.ID)
	}
	var doc database.TenantInventoryDocument
	if err := db.First(&doc, *mov.InventoryDocumentID).Error; err != nil {
		t.Fatal(err)
	}
	if doc.Source != DocumentSourceAdjustment {
		t.Fatalf("source = %q want %q", doc.Source, DocumentSourceAdjustment)
	}
}

func TestCreateInventoryDocumentSourceManual(t *testing.T) {
	db := setupDocumentTestDB(t)
	var branch database.TenantBranch
	db.First(&branch)
	p := seedProduct(t, db, branch.ID)
	opID := otherInTypeID(t, db)

	docSvc := NewInventoryDocumentService(db)
	docID, err := docSvc.CreateInventoryDocument(CreateDocumentInput{
		Direction: "IN", OperationTypeID: opID, BranchID: branch.ID,
		Lines:  []DocumentLineInput{{ProductID: p.ID, Quantity: 1, UnitCost: 2}},
		UserID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc database.TenantInventoryDocument
	if err := db.First(&doc, docID).Error; err != nil {
		t.Fatal(err)
	}
	if doc.Source != DocumentSourceManual {
		t.Fatalf("source = %q want %q", doc.Source, DocumentSourceManual)
	}
}

func TestUpdateDraftOnly(t *testing.T) {
	db := setupDocumentTestDB(t)
	var branch database.TenantBranch
	db.First(&branch)
	p := seedProduct(t, db, branch.ID)
	opID := otherInTypeID(t, db)

	docSvc := NewInventoryDocumentService(db)
	docID, _ := docSvc.CreateInventoryDocument(CreateDocumentInput{
		Direction: "IN", OperationTypeID: opID, BranchID: branch.ID,
		Lines:  []DocumentLineInput{{ProductID: p.ID, Quantity: 1, UnitCost: 2}},
		UserID: 1,
	})
	if err := docSvc.UpdateInventoryDocument(docID, UpdateDocumentInput{
		OperationTypeID: opID,
		Lines:           []DocumentLineInput{{ProductID: p.ID, Quantity: 4, UnitCost: 2.5}},
	}); err != nil {
		t.Fatal(err)
	}
	var line database.TenantInventoryDocumentDetail
	db.Where("document_id = ?", docID).First(&line)
	if line.Quantity != 4 {
		t.Fatalf("qty = %v", line.Quantity)
	}

	db.Model(&database.TenantInventoryDocument{}).Where("id = ?", docID).Update("status", DocumentStatusConfirmed)
	err := docSvc.UpdateInventoryDocument(docID, UpdateDocumentInput{
		OperationTypeID: opID,
		Lines:           []DocumentLineInput{{ProductID: p.ID, Quantity: 1}},
	})
	if err != ErrDocumentAlreadyConfirmed {
		t.Fatalf("update confirmed: %v", err)
	}
}

func TestRecordAdjustmentViaDocument_usesCustomSeriesPerBranch(t *testing.T) {
	db := setupDocumentTestDB(t)
	var branch database.TenantBranch
	db.First(&branch)
	p := seedProduct(t, db, branch.ID)

	if err := db.Where("branch_id = ? AND category = ?", branch.ID, "almacen").
		Delete(&database.TenantDocumentSeries{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantDocumentSeries{
		BranchID: branch.ID, DocType: "INGRESO_INVENTARIO", SunatCode: "00", Category: "almacen",
		Series: "ING002", Correlative: 1, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	inv := NewInventoryService(db)
	if err := inv.RecordAdjustment(AdjustmentInput{
		ProductID: p.ID, BranchID: branch.ID, Type: "in", Quantity: 6, Notes: "ING002 branch",
	}, 1); err != nil {
		t.Fatalf("adjustment ING002: %v", err)
	}

	var doc database.TenantInventoryDocument
	if err := db.Order("id DESC").First(&doc).Error; err != nil {
		t.Fatal(err)
	}
	if doc.Number != "ING002-00000001" {
		t.Fatalf("number = %q want ING002-00000001", doc.Number)
	}
}

func TestRecordAdjustmentViaDocument_autoSeedMissingSeries(t *testing.T) {
	db := setupDocumentTestDB(t)
	var branch database.TenantBranch
	db.First(&branch)
	p := seedProduct(t, db, branch.ID)

	if err := db.Where("branch_id = ? AND category = ?", branch.ID, "almacen").
		Delete(&database.TenantDocumentSeries{}).Error; err != nil {
		t.Fatal(err)
	}

	inv := NewInventoryService(db)
	if err := inv.RecordAdjustment(AdjustmentInput{
		ProductID: p.ID, BranchID: branch.ID, Type: "in", Quantity: 4, Notes: "Auto seed",
	}, 1); err != nil {
		t.Fatalf("adjustment auto-seed: %v", err)
	}

	var n int64
	db.Model(&database.TenantDocumentSeries{}).
		Where("branch_id = ? AND series = ? AND category = ?", branch.ID, "ING001", "almacen").
		Count(&n)
	if n != 1 {
		t.Fatalf("ING001 count = %d want 1", n)
	}
}
