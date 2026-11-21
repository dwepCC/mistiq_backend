package service

import (
	"testing"

	"tukifac/pkg/database"
)

// Fase 5, caso K: una compra de un producto CON atributos descriptivos funciona exactamente
// igual — el atributo no altera cantidad, costo, conversión ni Kardex.
func TestPurchaseSaleUnit_ProductWithAttributes_PurchasesExactlyTheSame(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, saco := seedArrozPurchase(t, db)
	if err := db.Create(&database.TenantProductAttribute{
		ProductID: p.ID, Name: "Origen", Value: "Nacional", Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	pid, suid := p.ID, saco.ID

	purchase, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &suid, Description: "Arroz Superior", Unit: "NIU",
		Quantity: 10, UnitCost: 350, IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatalf("un producto con atributos debe comprarse exactamente igual: %v", err)
	}
	if purchase.Total != 3500 {
		t.Errorf("total = %.2f, want 3500 (los atributos no deben afectar el cálculo)", purchase.Total)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 1000 {
		t.Errorf("stock = %g, want 1000 (igual que sin atributos)", stock.Quantity)
	}
	var attrCount int64
	db.Model(&database.TenantProductAttribute{}).Where("product_id = ?", p.ID).Count(&attrCount)
	if attrCount != 1 {
		t.Errorf("el atributo del producto no debe desaparecer por la compra, got %d", attrCount)
	}
}
