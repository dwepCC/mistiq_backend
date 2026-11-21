package service

import (
	"testing"

	"tukifac/pkg/database"
)

// Fase 7G — sección 13 del encargo: combina en un solo flujo lo que antes solo se probaba por
// separado (que el snapshot sobrevive a un cambio de factor, y que una devolución preserva el
// snapshot correctamente) — vender con factor histórico 12, cambiar el factor ACTUAL de la
// SaleUnit a 10, y confirmar que la devolución (completa y parcial) sigue reponiendo stock según
// el factor de 12, nunca el de 10.

func TestSaleUnitIntegration_FactorChangedAfterSale_FullReturn_UsesHistoricalFactor(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, saco := seedArroz(t, db, 1000) // seedArroz crea la SaleUnit "Saco" con factor 100
	series := seedNVSeriesSU(t, db)
	pid, suid := p.ID, saco.ID

	sale, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// El factor CAMBIA después de la venta: de 100 a 80.
	if err := db.Model(&database.TenantProductSaleUnit{}).Where("id = ?", suid).
		Update("conversion_factor", 80).Error; err != nil {
		t.Fatal(err)
	}

	if err := NewSaleService(db).CancelNotaVenta(sale.ID, 1, "anulación con factor ya cambiado"); err != nil {
		t.Fatalf("CancelNotaVenta: %v", err)
	}

	var reversal database.TenantStockMovement
	if err := db.Where("product_id = ? AND type = ?", p.ID, "in").First(&reversal).Error; err != nil {
		t.Fatal(err)
	}
	// Debe reponer 100 KG (factor histórico de la venta), NUNCA 80 (factor actual tras el cambio).
	if reversal.Quantity != 100 {
		t.Errorf("Quantity repuesto = %g, want 100 (factor histórico 100, no el factor actual 80)", reversal.Quantity)
	}
	if reversal.ConversionFactor == nil || *reversal.ConversionFactor != 100 {
		t.Errorf("ConversionFactor de la reversión = %v, want 100 (histórico, no 80)", reversal.ConversionFactor)
	}

	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 1000 {
		t.Errorf("stock final = %g, want 1000 (900 tras vender + 100 repuestos = stock original)", stock.Quantity)
	}
}

func TestSaleUnitIntegration_FactorChangedAfterSale_PartialReturn_UsesHistoricalFactor(t *testing.T) {
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

	// El factor CAMBIA después de la venta: de 100 a 80.
	if err := db.Model(&database.TenantProductSaleUnit{}).Where("id = ?", suid).
		Update("conversion_factor", 80).Error; err != nil {
		t.Fatal(err)
	}

	// Nota de crédito parcial: devuelve 1 de los 2 sacos vendidos (ratio 0.5).
	noteSale := database.TenantSale{Number: "NC01-1", DocType: "NOTA_CREDITO", BranchID: 1, UserID: 1}
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
		t.Fatalf("RestorePartialStockFromKardexTx: %v", err)
	}

	var reversal database.TenantStockMovement
	if err := db.Where("product_id = ? AND type = ?", p.ID, "in").First(&reversal).Error; err != nil {
		t.Fatal(err)
	}
	// 2 sacos vendidos = 200 KG de salida (factor histórico 100); devolver 1/2 = 50% → 100 KG.
	// Con el factor actual (80) habría dado 80 KG — confirmamos que NO se usa ese valor.
	if reversal.Quantity != 100 {
		t.Errorf("Quantity repuesto = %g, want 100 (50%% de 200 KG con factor histórico 100, no 80 con el factor actual)", reversal.Quantity)
	}
	if reversal.ConversionFactor == nil || *reversal.ConversionFactor != 100 {
		t.Errorf("ConversionFactor de la reversión = %v, want 100 (histórico)", reversal.ConversionFactor)
	}
	if reversal.SaleUnitQuantity == nil || *reversal.SaleUnitQuantity != 1 {
		t.Errorf("SaleUnitQuantity de la reversión = %v, want 1 (comercial, 50%% de 2 sacos)", reversal.SaleUnitQuantity)
	}
}
