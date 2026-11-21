package service

import (
	"testing"

	"tukifac/pkg/database"
)

// Una nota de crédito parcial ya repuso stock; la anulación total posterior no debe volver a
// reponer esa parte (antes: stock final inflado por la cantidad de la nota parcial).
func TestCancelAfterPartialCreditNote_DoesNotDoubleRestoreStock(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, saco := seedArroz(t, db, 1000)
	series := seedNVSeriesSU(t, db)
	pid, suid := p.ID, saco.ID

	sale, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 2, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var originalItem database.TenantSaleItem
	if err := db.Where("sale_id = ?", sale.ID).First(&originalItem).Error; err != nil {
		t.Fatal(err)
	}

	origID := sale.ID
	noteSale := database.TenantSale{Number: "NC01-1", DocType: "NOTA_CREDITO", BranchID: 1, UserID: 1, OriginalSaleID: &origID}
	if err := db.Create(&noteSale).Error; err != nil {
		t.Fatal(err)
	}
	origItemID := originalItem.ID
	if err := db.Create(&database.TenantSaleItem{
		SaleID: noteSale.ID, ProductID: &pid, Description: "Devolución parcial", Unit: "NIU",
		Quantity: 1, UnitPrice: 450, Subtotal: 381.36, TaxAmount: 68.64, Total: 450,
		OriginalSaleItemID: &origItemID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := RestorePartialStockFromKardexTx(db, &noteSale, "NC/"+noteSale.Number, 1); err != nil {
		t.Fatal(err)
	}

	if err := NewSaleService(db).CancelNotaVenta(sale.ID, 1, "anulación total tras parcial"); err != nil {
		t.Fatalf("CancelNotaVenta: %v", err)
	}

	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 1000 {
		t.Errorf("stock final = %g, want 1000 (sin doble reposición)", stock.Quantity)
	}
}
