package service

import (
	"testing"

	"tukifac/pkg/database"
)

// Fase 5, caso J: una venta de un producto CON atributos descriptivos funciona exactamente igual
// que sin ellos — el atributo no altera quantity, unit_price, SaleUnit, stock ni Kardex.
func TestSaleUnitIntegration_ProductWithAttributes_SellsExactlyTheSame(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, saco := seedArroz(t, db, 1000)
	if err := db.Create(&database.TenantProductAttribute{
		ProductID: p.ID, Name: "Origen", Value: "Nacional", Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductAttribute{
		ProductID: p.ID, Name: "Marca", Value: "Sustento", Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	series := seedNVSeriesSU(t, db)

	pid, suid := p.ID, saco.ID
	sale, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1.5, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatalf("un producto con atributos debe venderse exactamente igual: %v", err)
	}
	if sale.Total != 675 {
		t.Errorf("total = %.2f, want 675 (los atributos no deben afectar el cálculo)", sale.Total)
	}

	var item database.TenantSaleItem
	db.Where("sale_id = ?", sale.ID).First(&item)
	if item.Quantity != 1.5 || item.UnitPrice != 450 {
		t.Errorf("quantity/unit_price alterados por la presencia de atributos: %+v", item)
	}

	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 850 {
		t.Errorf("stock = %g, want 850 (igual que sin atributos)", stock.Quantity)
	}

	var attrCount int64
	db.Model(&database.TenantProductAttribute{}).Where("product_id = ?", p.ID).Count(&attrCount)
	if attrCount != 2 {
		t.Errorf("los atributos del producto no deben desaparecer ni duplicarse por la venta, got %d", attrCount)
	}
}
