package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"
)

// TestFullIntegration_Attribute_SaleUnit_BranchPrice_Sale_Return es el test integral pedido en la
// corrección 2 de Fase 6.1: demuestra que Attribute + SaleUnit + BranchPrice conviven en el mismo
// producto sin interferirse, atravesando una venta real (con precio de sucursal, conversión de
// cantidad y Kardex) y una devolución parcial que preserva el snapshot histórico.
func TestFullIntegration_Attribute_SaleUnit_BranchPrice_Sale_Return(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)

	// ---- Setup: Producto P + Attribute + SaleUnit + BranchPrice ----
	product := database.TenantProduct{
		Code: "P-INTEGRAL", Name: "Producto integral", Type: "product", Unit: "NIU", SalePrice: 25,
		ManageStock: true, IgvAffectationType: "10", PriceIncludesIgv: true, Active: true,
	}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductStock{ProductID: product.ID, BranchID: 1, Quantity: 1000}).Error; err != nil {
		t.Fatal(err)
	}
	attribute := database.TenantProductAttribute{
		ProductID: product.ID, Name: "Color", Value: "Rojo", Active: true,
	}
	if err := db.Create(&attribute).Error; err != nil {
		t.Fatal(err)
	}
	caja := database.TenantProductSaleUnit{
		ProductID: product.ID, Name: "Caja x 12", ConversionFactor: 12, AllowFraction: true,
		Price1: 34, Active: true,
	}
	if err := db.Create(&caja).Error; err != nil {
		t.Fatal(err)
	}
	branchA := database.TenantBranch{Name: "Sucursal A", Active: true}
	if err := db.Create(&branchA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCashSession{
		BranchID: branchA.ID, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	branchPrice := database.TenantProductSaleUnitBranchPrice{
		SaleUnitID: caja.ID, BranchID: branchA.ID, Price1: 36, Active: true,
	}
	if err := db.Create(&branchPrice).Error; err != nil {
		t.Fatal(err)
	}
	series := database.TenantDocumentSeries{
		BranchID: branchA.ID, DocType: "Nota de Venta", SunatCode: "00", Series: "NVI1", Correlative: 1, Active: true,
	}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}

	// ---- Venta: 2 Cajas x 12 en Sucursal A ----
	pid, suid := product.ID, caja.ID
	sale, err := NewSaleService(db).Create(CreateSaleInput{
		BranchID: branchA.ID, UserID: 1, SeriesID: series.ID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: 72}}, // 2 × 36
		Items: []SaleItemInput{{
			ProductID: &pid, SaleUnitID: &suid, Quantity: 2, UnitPrice: 36,
			Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
		}},
	})
	if err != nil {
		t.Fatalf("Create (venta integral): %v", err)
	}

	// Precio: 36 (override de sucursal), NO 34 (global) ni 432 (36×12).
	var item database.TenantSaleItem
	if err := db.Where("sale_id = ?", sale.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.UnitPrice != 36 {
		t.Errorf("UnitPrice = %.2f, want 36 (override de Sucursal A, no el global 34 ni 34×12)", item.UnitPrice)
	}
	// Cantidad comercial: 2.
	if item.Quantity != 2 {
		t.Errorf("Quantity (comercial) = %.2f, want 2", item.Quantity)
	}
	// SaleUnitID de referencia histórica.
	if item.SaleUnitID == nil || *item.SaleUnitID != suid {
		t.Errorf("SaleItem.SaleUnitID = %v, want %d", item.SaleUnitID, suid)
	}
	if sale.Total != 72 {
		t.Errorf("Total de la venta = %.2f, want 72 (2 × 36, nunca 2×34 ni 36×12)", sale.Total)
	}

	// Kardex: cantidad base = 2 × 12 = 24, con snapshot completo.
	var movements []database.TenantStockMovement
	if err := db.Where("product_id = ?", product.ID).Find(&movements).Error; err != nil {
		t.Fatal(err)
	}
	if len(movements) != 1 {
		t.Fatalf("esperaba exactamente 1 movimiento de Kardex por la venta, got %d", len(movements))
	}
	mv := movements[0]
	if mv.Quantity != 24 {
		t.Errorf("Kardex.Quantity (base) = %g, want 24 (2 × 12)", mv.Quantity)
	}
	if mv.SaleUnitQuantity == nil || *mv.SaleUnitQuantity != 2 {
		t.Errorf("Kardex.SaleUnitQuantity = %v, want 2", mv.SaleUnitQuantity)
	}
	if mv.ConversionFactor == nil || *mv.ConversionFactor != 12 {
		t.Errorf("Kardex.ConversionFactor = %v, want 12", mv.ConversionFactor)
	}

	// Stock: 1000 - 24 = 976.
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", product.ID, branchA.ID).First(&stock)
	if stock.Quantity != 976 {
		t.Errorf("stock = %g, want 976 (1000 - 24)", stock.Quantity)
	}

	// Attribute: sigue asociado al producto, sin duplicarse ni desaparecer, y sin haber generado
	// stock/movimiento/precio/SaleUnit adicionales por su sola presencia.
	var attrs []database.TenantProductAttribute
	db.Where("product_id = ?", product.ID).Find(&attrs)
	if len(attrs) != 1 || attrs[0].Name != "Color" || attrs[0].Value != "Rojo" {
		t.Fatalf("el atributo Color=Rojo debe seguir asociado tal cual, got %+v", attrs)
	}
	var saleUnitsCount int64
	db.Model(&database.TenantProductSaleUnit{}).Where("product_id = ?", product.ID).Count(&saleUnitsCount)
	if saleUnitsCount != 1 {
		t.Errorf("el atributo no debe generar SaleUnits adicionales, got %d", saleUnitsCount)
	}

	// ---- Devolución parcial: 1 Caja x 12 ----
	originalItemID := item.ID
	noteSale := database.TenantSale{Number: "NCI1-1", DocType: "NOTA_CREDITO", BranchID: branchA.ID, UserID: 1}
	if err := db.Create(&noteSale).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSaleItem{
		SaleID: noteSale.ID, ProductID: &pid, Description: "Devolución parcial", Unit: "NIU",
		Quantity: 1, UnitPrice: 36, Subtotal: 30.51, TaxAmount: 5.49, Total: 36,
		OriginalSaleItemID: &originalItemID,
	}).Error; err != nil {
		t.Fatal(err)
	}

	// Cambiar el precio de sucursal DESPUÉS de la venta y ANTES de procesar la devolución, para
	// demostrar que ninguno de los dos (venta original ni devolución) se recalcula con él.
	if err := db.Model(&branchPrice).Update("price1", 40).Error; err != nil {
		t.Fatal(err)
	}

	if err := RestorePartialStockFromKardexTx(db, &noteSale, "NC/"+noteSale.Number, 1); err != nil {
		t.Fatalf("RestorePartialStockFromKardexTx: %v", err)
	}

	// Cantidad base revertida: 1 (comercial) × 12 (factor histórico) = 12. NO 1, NO 24.
	var reversal database.TenantStockMovement
	if err := db.Where("product_id = ? AND type = ?", product.ID, "in").First(&reversal).Error; err != nil {
		t.Fatal(err)
	}
	if reversal.Quantity != 12 {
		t.Errorf("Quantity de la reversión = %g, want 12 (1 caja × factor histórico 12)", reversal.Quantity)
	}
	if reversal.SaleUnitQuantity == nil || *reversal.SaleUnitQuantity != 1 {
		t.Errorf("SaleUnitQuantity de la reversión = %v, want 1 (cantidad comercial devuelta)", reversal.SaleUnitQuantity)
	}
	if reversal.ConversionFactor == nil || *reversal.ConversionFactor != 12 {
		t.Errorf("ConversionFactor de la reversión = %v, want 12 (snapshot histórico, no el actual)", reversal.ConversionFactor)
	}

	var stockAfterReturn database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", product.ID, branchA.ID).First(&stockAfterReturn)
	if stockAfterReturn.Quantity != 988 {
		t.Errorf("stock tras la devolución = %g, want 988 (976 + 12)", stockAfterReturn.Quantity)
	}

	// El precio histórico de la venta original NO se recalcula, aunque el BranchPrice haya
	// cambiado (36 → 40) entre la venta y la devolución.
	var reloadedOriginalItem database.TenantSaleItem
	db.First(&reloadedOriginalItem, originalItemID)
	if reloadedOriginalItem.UnitPrice != 36 {
		t.Errorf("el precio histórico de la venta cambió: got %.2f, want 36 (no debe recalcularse con el BranchPrice actual, 40)", reloadedOriginalItem.UnitPrice)
	}

	// Una venta NUEVA en la misma sucursal sí usa el precio ya actualizado (40).
	newSale, err := NewSaleService(db).Create(CreateSaleInput{
		BranchID: branchA.ID, UserID: 1, SeriesID: series.ID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: 40}},
		Items: []SaleItemInput{{
			ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 40,
			Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
		}},
	})
	if err != nil {
		t.Fatalf("la venta nueva debe poder usar el precio ya actualizado (40): %v", err)
	}
	if newSale.Total != 40 {
		t.Errorf("venta nueva: total = %.2f, want 40", newSale.Total)
	}
}
