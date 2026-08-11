package service

import (
	"testing"

	"tukifac/pkg/database"
)

func TestPreviewImportAdjustment_Basic(t *testing.T) {
	db := setupDocumentTestDB(t)
	var branch database.TenantBranch
	db.First(&branch)
	p := seedProduct(t, db, branch.ID)
	p.Code = "7750123456789"
	p.PurchasePrice = 4.5
	db.Save(&p)
	db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: branch.ID, Quantity: 10})

	svc := NewInventoryDocumentService(db)
	result, err := svc.PreviewImportAdjustment(ImportAdjustmentPreviewInput{
		BranchID: branch.ID,
		Rows: []ImportAdjustmentRowInput{
			{RowNumber: 2, Barcode: "7750123456789", NewStock: 20},
			{RowNumber: 3, Barcode: "7750123456789", NewStock: 5},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.ErrorRows != 1 {
		t.Fatalf("errors = %d want 1 (duplicate)", result.Summary.ErrorRows)
	}
	if result.Summary.ValidInRows != 1 {
		t.Fatalf("valid in = %d want 1", result.Summary.ValidInRows)
	}
	if result.Summary.TotalInQty != 10 {
		t.Fatalf("in qty = %v want 10", result.Summary.TotalInQty)
	}
	if result.CanConfirm {
		t.Fatal("should not confirm while duplicate error exists")
	}
}

func TestConfirmImportAdjustment_TwoDocuments(t *testing.T) {
	db := setupDocumentTestDB(t)
	var branch database.TenantBranch
	db.First(&branch)

	pIn := seedProduct(t, db, branch.ID)
	pIn.Code = "IN001"
	pIn.PurchasePrice = 2
	db.Save(&pIn)
	db.Create(&database.TenantProductStock{ProductID: pIn.ID, BranchID: branch.ID, Quantity: 5})

	pOut := seedProduct(t, db, branch.ID)
	pOut.Code = "OUT001"
	pOut.PurchasePrice = 3
	db.Save(&pOut)
	db.Create(&database.TenantProductStock{ProductID: pOut.ID, BranchID: branch.ID, Quantity: 10})

	svc := NewInventoryDocumentService(db)
	result, err := svc.ConfirmImportAdjustment(ImportAdjustmentConfirmInput{
		BranchID:       branch.ID,
		MovementReason: "Conteo físico",
		Rows: []ImportAdjustmentRowInput{
			{RowNumber: 2, Barcode: "IN001", NewStock: 8},
			{RowNumber: 3, Barcode: "OUT001", NewStock: 7},
		},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.InDocumentID == nil || result.OutDocumentID == nil {
		t.Fatal("expected both documents")
	}

	var inDoc, outDoc database.TenantInventoryDocument
	db.First(&inDoc, *result.InDocumentID)
	db.First(&outDoc, *result.OutDocumentID)
	if inDoc.Source != DocumentSourceImport || outDoc.Source != DocumentSourceImport {
		t.Fatalf("source in=%q out=%q", inDoc.Source, outDoc.Source)
	}
	if inDoc.Status != DocumentStatusConfirmed || outDoc.Status != DocumentStatusConfirmed {
		t.Fatal("expected confirmed documents")
	}

	var stockIn, stockOut database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", pIn.ID, branch.ID).First(&stockIn)
	db.Where("product_id = ? AND branch_id = ?", pOut.ID, branch.ID).First(&stockOut)
	if stockIn.Quantity != 8 || stockOut.Quantity != 7 {
		t.Fatalf("stock in=%v out=%v", stockIn.Quantity, stockOut.Quantity)
	}
}

func TestPreviewImportAdjustment_ProductNotFound(t *testing.T) {
	db := setupDocumentTestDB(t)
	var branch database.TenantBranch
	db.First(&branch)
	svc := NewInventoryDocumentService(db)
	result, err := svc.PreviewImportAdjustment(ImportAdjustmentPreviewInput{
		BranchID: branch.ID,
		Rows:     []ImportAdjustmentRowInput{{RowNumber: 2, Barcode: "NOEXISTE", NewStock: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CanConfirm {
		t.Fatal("should not confirm with errors")
	}
	if result.Summary.ErrorRows != 1 {
		t.Fatalf("errors = %d", result.Summary.ErrorRows)
	}
}

func TestWeightedAverageUnitCosts(t *testing.T) {
	db := setupDocumentTestDB(t)
	var branch database.TenantBranch
	db.First(&branch)
	p := seedProduct(t, db, branch.ID)
	p.PurchasePrice = 1
	db.Save(&p)

	inv := NewInventoryService(db)
	if err := inv.RecordMovement(MovementInput{
		ProductID: p.ID, BranchID: branch.ID, Type: "in", Quantity: 10, UnitCost: 5, UserID: 1,
		OperationCode: "PURCHASE",
	}); err != nil {
		t.Fatal(err)
	}
	if err := inv.RecordMovement(MovementInput{
		ProductID: p.ID, BranchID: branch.ID, Type: "in", Quantity: 10, UnitCost: 7, UserID: 1,
		OperationCode: "PURCHASE",
	}); err != nil {
		t.Fatal(err)
	}

	avg, err := inv.WeightedAverageUnitCosts([]uint{p.ID}, branch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if avg[p.ID] != 6 {
		t.Fatalf("avg = %v want 6", avg[p.ID])
	}
}
