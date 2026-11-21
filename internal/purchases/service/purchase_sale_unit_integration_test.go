package service

import (
	"fmt"
	"math"
	"testing"
	"time"

	invsvc "tukifac/internal/inventory/service"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupPurchaseSaleUnitDB extiende setupPurchaseServiceTestDB con TenantProductSaleUnit.
func setupPurchaseSaleUnitDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantProduct{}, &database.TenantPurchase{}, &database.TenantPurchaseItem{},
		&database.TenantProductStock{}, &database.TenantStockMovement{},
		&database.TenantInventoryOperationType{}, &database.TenantProductSerial{},
		&database.TenantBankAccount{}, &database.TenantBankMovement{}, &database.TenantPaymentMethod{},
		&database.TenantCashSession{}, &database.TenantCashMovement{},
		&database.TenantPurchasePayable{}, &database.TenantPurchasePayment{},
		&database.TenantProductSaleUnit{}, &database.TenantProductAttribute{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.SeedInventoryOperationTypes(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedArrozPurchase(t *testing.T, db *gorm.DB) (product database.TenantProduct, saco100 database.TenantProductSaleUnit) {
	t.Helper()
	product = database.TenantProduct{
		Code: "ARR-KG", Name: "Arroz Superior", Type: "product", Unit: "KGM",
		SalePrice: 5, ManageStock: true, IgvAffectationType: "10", Active: true,
	}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	saco100 = database.TenantProductSaleUnit{
		ProductID: product.ID, Name: "Saco 100 KG", ConversionFactor: 100, AllowFraction: true,
		Price1: 4.5, Active: true,
	}
	if err := db.Create(&saco100).Error; err != nil {
		t.Fatal(err)
	}
	return product, saco100
}

func createPurchaseSU(t *testing.T, db *gorm.DB, item PurchaseItemInput) (*database.TenantPurchase, error) {
	t.Helper()
	newOpenCashSession(t, db, 1, 1)
	return NewPurchaseService(db).Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001",
		Number:    fmt.Sprintf("%d", time.Now().UnixNano()),
		IssueDate: time.Now(), Currency: "PEN",
		Items:     []PurchaseItemInput{item},
		TaxConfig: tax.Config{TaxRate: 18},
	})
}

// ---- Legacy (casos 1-4) ----

func TestPurchaseSaleUnit_Legacy_WorksAsBefore(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	newOpenCashSession(t, db, 1, 1)
	product := database.TenantProduct{
		Code: "LEG-KG", Name: "Producto legacy", Type: "product", Unit: "KGM",
		SalePrice: 10, ManageStock: true, IgvAffectationType: "10", Active: true,
	}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	pid := product.ID
	_, err := NewPurchaseService(db).Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "1",
		IssueDate: time.Now(), Currency: "PEN",
		Items: []PurchaseItemInput{{
			ProductID: &pid, Description: "Producto legacy", Unit: "KGM", Quantity: 100, UnitCost: 3,
			IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", product.ID, 1).First(&stock)
	if stock.Quantity != 100 {
		t.Errorf("stock = %g, want 100 (1:1, sin conversión)", stock.Quantity)
	}
	var reloaded database.TenantProduct
	db.First(&reloaded, product.ID)
	if reloaded.PurchasePrice != 3 {
		t.Errorf("purchase_price = %.2f, want 3.00 (costo sin cambios)", reloaded.PurchasePrice)
	}
	var mv database.TenantStockMovement
	db.Where("product_id = ?", product.ID).First(&mv)
	if mv.SaleUnitID != nil {
		t.Errorf("una compra legacy no debe tener SaleUnitID en el kardex, got %v", mv.SaleUnitID)
	}
}

// ---- SaleUnit (casos 5-10) ----

func TestPurchaseSaleUnit_BaseUnit_FactorOne(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, _ := seedArrozPurchase(t, db)
	kg := database.TenantProductSaleUnit{
		ProductID: p.ID, Name: "KG", ConversionFactor: 1, IsBase: true, AllowFraction: true, Price1: 4.5, Active: true,
	}
	if err := db.Create(&kg).Error; err != nil {
		t.Fatal(err)
	}
	pid, kgID := p.ID, kg.ID
	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &kgID, Description: "Arroz", Unit: "KGM", Quantity: 20, UnitCost: 3,
		IgvAffectationType: "10",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 20 {
		t.Errorf("stock = %g, want 20 (unidad base, factor 1)", stock.Quantity)
	}
}

func TestPurchaseSaleUnit_Saco50KG(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, _ := seedArrozPurchase(t, db)
	saco50 := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Saco 50 KG", ConversionFactor: 50, AllowFraction: true, Price1: 2.5, Active: true}
	if err := db.Create(&saco50).Error; err != nil {
		t.Fatal(err)
	}
	pid, suid := p.ID, saco50.ID
	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &suid, Description: "Arroz", Unit: "KGM", Quantity: 1, UnitCost: 175,
		IgvAffectationType: "10",
	}); err != nil {
		t.Fatal(err)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 50 {
		t.Errorf("stock = %g, want 50 (1 saco × 50)", stock.Quantity)
	}
}

func TestPurchaseSaleUnit_Saco100KG_Single(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, saco := seedArrozPurchase(t, db)
	pid, suid := p.ID, saco.ID
	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &suid, Description: "Arroz", Unit: "KGM", Quantity: 1, UnitCost: 350,
		IgvAffectationType: "10",
	}); err != nil {
		t.Fatal(err)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 100 {
		t.Errorf("stock = %g, want 100 (1 saco × 100)", stock.Quantity)
	}
}

// AllowFraction=true acepta 1.5 sacos.
func TestPurchaseSaleUnit_FractionalQuantity_AllowedWhenAllowFractionTrue(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, saco := seedArrozPurchase(t, db)
	pid, suid := p.ID, saco.ID
	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &suid, Description: "Arroz", Unit: "KGM", Quantity: 1.5, UnitCost: 350,
		IgvAffectationType: "10",
	}); err != nil {
		t.Fatalf("1.5 sacos con AllowFraction=true debe aceptarse: %v", err)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 150 {
		t.Errorf("stock = %g, want 150 (1.5 × 100)", stock.Quantity)
	}
}

// AllowFraction=false rechaza 1.5 pero acepta enteros.
func TestPurchaseSaleUnit_AllowFractionFalse_RejectsFraction(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p := database.TenantProduct{
		Code: "CAJ-U", Name: "Gaseosa", Type: "product", Unit: "NIU", SalePrice: 3,
		ManageStock: true, IgvAffectationType: "10", Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	caja := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Caja x24", ConversionFactor: 24, AllowFraction: false, Price1: 60, Active: true}
	if err := db.Create(&caja).Error; err != nil {
		t.Fatal(err)
	}
	pid, cajaID := p.ID, caja.ID

	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &cajaID, Description: "Gaseosa", Unit: "NIU", Quantity: 1.5, UnitCost: 100,
		IgvAffectationType: "10",
	}); err == nil {
		t.Fatal("esperaba rechazo: 1.5 cajas con AllowFraction=false")
	}
	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &cajaID, Description: "Gaseosa", Unit: "NIU", Quantity: 2, UnitCost: 100,
		IgvAffectationType: "10",
	}); err != nil {
		t.Fatalf("2 cajas enteras debe aceptarse: %v", err)
	}
}

// ---- Cantidad (casos 11-12) ----

func TestPurchaseSaleUnit_Quantity_MustBePositive(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, saco := seedArrozPurchase(t, db)
	pid, suid := p.ID, saco.ID
	for _, q := range []float64{0, -5} {
		if _, err := createPurchaseSU(t, db, PurchaseItemInput{
			ProductID: &pid, SaleUnitID: &suid, Description: "Arroz", Unit: "KGM", Quantity: q, UnitCost: 350,
			IgvAffectationType: "10",
		}); err == nil {
			t.Errorf("quantity=%.1f debería rechazarse", q)
		}
	}
}

// ---- Integridad (casos 13-15) ----

func TestPurchaseSaleUnit_OtherProduct_Rejected(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p1, _ := seedArrozPurchase(t, db)
	_, saco2 := seedArrozPurchase(t, db)
	p1id, otherSU := p1.ID, saco2.ID
	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &p1id, SaleUnitID: &otherSU, Description: "Arroz", Unit: "KGM", Quantity: 1, UnitCost: 350,
		IgvAffectationType: "10",
	}); err == nil {
		t.Fatal("esperaba rechazo: SaleUnit pertenece a otro producto")
	}
}

func TestPurchaseSaleUnit_NonExistent_Rejected(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, _ := seedArrozPurchase(t, db)
	pid := p.ID
	ghost := uint(99999)
	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &ghost, Description: "Arroz", Unit: "KGM", Quantity: 1, UnitCost: 350,
		IgvAffectationType: "10",
	}); err == nil {
		t.Fatal("esperaba rechazo: SaleUnit inexistente")
	}
}

func TestPurchaseSaleUnit_Inactive_Rejected(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, saco := seedArrozPurchase(t, db)
	if err := db.Model(&saco).Update("active", false).Error; err != nil {
		t.Fatal(err)
	}
	pid, suid := p.ID, saco.ID
	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &suid, Description: "Arroz", Unit: "KGM", Quantity: 1, UnitCost: 350,
		IgvAffectationType: "10",
	}); err == nil {
		t.Fatal("esperaba rechazo: SaleUnit inactiva")
	}
}

// ---- Inventario (casos 16-18) ----

// Caso 16/17 se cubren en el test end-to-end de abajo (10 sacos, un solo movimiento).

// Caso 18: dos compras sucesivas con SaleUnit no corrompen el stock acumulado.
func TestPurchaseSaleUnit_MultiplePurchases_NoCorruption(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, saco := seedArrozPurchase(t, db)
	pid, suid := p.ID, saco.ID

	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &suid, Description: "Arroz", Unit: "KGM", Quantity: 5, UnitCost: 350,
		IgvAffectationType: "10",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &suid, Description: "Arroz", Unit: "KGM", Quantity: 3, UnitCost: 350,
		IgvAffectationType: "10",
	}); err != nil {
		t.Fatal(err)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 800 {
		t.Errorf("stock = %g, want 800 (500+300 KG)", stock.Quantity)
	}
	var movements []database.TenantStockMovement
	db.Where("product_id = ?", p.ID).Order("id ASC").Find(&movements)
	if len(movements) != 2 {
		t.Fatalf("esperaba 2 movimientos, got %d", len(movements))
	}
	if movements[0].Balance != 500 || movements[1].Balance != 800 {
		t.Errorf("balances = %g, %g; want 500, 800", movements[0].Balance, movements[1].Balance)
	}
}

// ---- Test end-to-end obligatorio (casos 8, 16, 17, 19, 20, 23, 28, 29, 30) ----

func TestPurchaseSaleUnit_EndToEnd_10SacosDe100KG(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, saco := seedArrozPurchase(t, db)
	pid, suid := p.ID, saco.ID

	purchase, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &suid, Description: "Arroz Superior", Unit: "NIU",
		Quantity: 10, UnitCost: 350, IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Documento: conserva cantidad y costo COMERCIALES (casos 28, 29).
	if purchase.Total != 3500 {
		t.Errorf("total del documento = %.2f, want 3500 (10 × 350, comercial)", purchase.Total)
	}
	var item database.TenantPurchaseItem
	db.Where("purchase_id = ?", purchase.ID).First(&item)
	if item.Quantity != 10 || item.UnitCost != 350 {
		t.Errorf("TenantPurchaseItem debe conservar cantidad/costo comercial: quantity=%.2f unit_cost=%.2f", item.Quantity, item.UnitCost)
	}
	if item.SaleUnitID == nil || *item.SaleUnitID != suid {
		t.Errorf("TenantPurchaseItem.SaleUnitID no persistido: %v", item.SaleUnitID)
	}

	// Inventario: cantidad BASE (casos 8, 16, 30).
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 1000 {
		t.Errorf("stock = %g, want 1000 KG", stock.Quantity)
	}

	// Kardex: un solo movimiento (caso 17), con snapshot completo (caso 23) y costo base (caso 19).
	var movements []database.TenantStockMovement
	db.Where("product_id = ?", p.ID).Find(&movements)
	if len(movements) != 1 {
		t.Fatalf("esperaba exactamente 1 movimiento, got %d", len(movements))
	}
	mv := movements[0]
	if mv.Quantity != 1000 {
		t.Errorf("Kardex.Quantity = %g, want 1000 (base, no 10 ni 3500)", mv.Quantity)
	}
	if mv.SaleUnitQuantity == nil || *mv.SaleUnitQuantity != 10 {
		t.Errorf("Kardex.SaleUnitQuantity = %v, want 10", mv.SaleUnitQuantity)
	}
	if mv.ConversionFactor == nil || *mv.ConversionFactor != 100 {
		t.Errorf("Kardex.ConversionFactor = %v, want 100", mv.ConversionFactor)
	}
	if math.Abs(mv.UnitCost-3.5) > 1e-9 {
		t.Errorf("Kardex.UnitCost = %.6f, want 3.50 (350/100)", mv.UnitCost)
	}
	if mv.PurchaseItemID == nil || *mv.PurchaseItemID != item.ID {
		t.Errorf("Kardex.PurchaseItemID no vincula a la línea de compra: %v", mv.PurchaseItemID)
	}

	// Catálogo: PurchasePrice queda en costo BASE, no comercial.
	var reloadedProduct database.TenantProduct
	db.First(&reloadedProduct, p.ID)
	if math.Abs(reloadedProduct.PurchasePrice-3.5) > 1e-9 {
		t.Errorf("PurchasePrice = %.6f, want 3.50 (base), no 350 (comercial)", reloadedProduct.PurchasePrice)
	}
}

// ---- Histórico (casos 23-24) ----

func TestPurchaseSaleUnit_HistoricalSnapshot_SurvivesFactorChange(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, saco := seedArrozPurchase(t, db)
	pid, suid := p.ID, saco.ID

	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &suid, Description: "Arroz", Unit: "KGM", Quantity: 10, UnitCost: 350,
		IgvAffectationType: "10",
	}); err != nil {
		t.Fatal(err)
	}

	// El saco ahora "pesa" 80 KG en vez de 100.
	if err := db.Model(&saco).Update("conversion_factor", 80).Error; err != nil {
		t.Fatal(err)
	}

	var mv database.TenantStockMovement
	db.Where("product_id = ?", p.ID).First(&mv)
	if mv.Quantity != 1000 {
		t.Errorf("el movimiento histórico debe seguir en 1000 KG, no reinterpretarse a 800 (10×80): got %g", mv.Quantity)
	}
	if mv.ConversionFactor == nil || *mv.ConversionFactor != 100 {
		t.Errorf("el snapshot de factor debe conservar 100 (el usado en la compra), got %v", mv.ConversionFactor)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 1000 {
		t.Errorf("el stock tampoco debe recalcularse: sigue en 1000, got %g", stock.Quantity)
	}
}

// ---- Reversión (casos 25, 27; no existe devolución parcial de compra — ver informe) ----

func TestPurchaseSaleUnit_Void_RestoresBaseQuantity(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, saco := seedArrozPurchase(t, db)
	pid, suid := p.ID, saco.ID

	purchase, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &suid, Description: "Arroz", Unit: "KGM", Quantity: 10, UnitCost: 350,
		IgvAffectationType: "10",
	})
	if err != nil {
		t.Fatal(err)
	}
	var afterPurchase database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&afterPurchase)
	if afterPurchase.Quantity != 1000 {
		t.Fatalf("precondición: stock debería ser 1000, got %g", afterPurchase.Quantity)
	}

	if err := NewPurchaseService(db).Void(purchase.ID, 1); err != nil {
		t.Fatalf("Void: %v", err)
	}

	var afterVoid database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&afterVoid)
	if afterVoid.Quantity != 0 {
		t.Errorf("stock tras anular = %g, want 0 (retira 1000 KG, la cantidad base histórica)", afterVoid.Quantity)
	}

	var reversal database.TenantStockMovement
	if err := db.Where("product_id = ? AND type = ?", p.ID, "out").First(&reversal).Error; err != nil {
		t.Fatal(err)
	}
	if reversal.SaleUnitID == nil || *reversal.SaleUnitID != suid {
		t.Errorf("la reversión debe conservar SaleUnitID, got %v", reversal.SaleUnitID)
	}
	if reversal.SaleUnitQuantity == nil || *reversal.SaleUnitQuantity != 10 {
		t.Errorf("la reversión debe conservar SaleUnitQuantity=10, got %v", reversal.SaleUnitQuantity)
	}
	if reversal.ConversionFactor == nil || *reversal.ConversionFactor != 100 {
		t.Errorf("la reversión debe conservar ConversionFactor=100, got %v", reversal.ConversionFactor)
	}
}

// Caso 27: cambiar el factor DESPUÉS de anular no debe alterar lo ya revertido (la reversión ya
// leyó el snapshot histórico del movimiento original, no el factor actual, en ningún momento).
func TestPurchaseSaleUnit_Void_ThenFactorChange_DoesNotAlterReversal(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, saco := seedArrozPurchase(t, db)
	pid, suid := p.ID, saco.ID

	purchase, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &suid, Description: "Arroz", Unit: "KGM", Quantity: 10, UnitCost: 350,
		IgvAffectationType: "10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewPurchaseService(db).Void(purchase.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&saco).Update("conversion_factor", 25).Error; err != nil {
		t.Fatal(err)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 0 {
		t.Errorf("el stock tras la reversión no debe recalcularse con el nuevo factor, sigue en 0, got %g", stock.Quantity)
	}
}

// ---- Costo (casos 19-22) ----

// Caso 21: el costo promedio ponderado sigue el algoritmo EXISTENTE
// (InventoryService.WeightedAverageUnitCosts), alimentado con cantidad/costo BASE ya convertidos.
func TestPurchaseSaleUnit_WeightedAverageCost_UsesExistingAlgorithm(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p, saco := seedArrozPurchase(t, db)
	pid, suid := p.ID, saco.ID

	// Stock previo: 500 KG a S/3.00/KG (compra legacy, sin SaleUnit).
	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, Description: "Arroz", Unit: "KGM", Quantity: 500, UnitCost: 3,
		IgvAffectationType: "10",
	}); err != nil {
		t.Fatal(err)
	}
	// Nueva compra: 10 sacos × 100 KG a S/350/saco → 1,000 KG a S/3.50/KG.
	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &suid, Description: "Arroz", Unit: "KGM", Quantity: 10, UnitCost: 350,
		IgvAffectationType: "10",
	}); err != nil {
		t.Fatal(err)
	}

	avgs, err := invsvc.NewInventoryService(db).WeightedAverageUnitCosts([]uint{p.ID}, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := (500*3.0 + 1000*3.5) / 1500.0 // = 3.333333...
	got := avgs[p.ID]
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("costo promedio = %.6f, want %.6f (algoritmo actual, sin fórmula alternativa)", got, want)
	}
}

// Caso 22: factores "incómodos" (24) no pierden precisión relevante gracias a decimal(15,6).
func TestPurchaseSaleUnit_Precision_AwkwardFactor(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	p := database.TenantProduct{
		Code: "CAJ24", Name: "Producto en caja", Type: "product", Unit: "NIU", SalePrice: 5,
		ManageStock: true, IgvAffectationType: "10", Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	caja := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Caja x24", ConversionFactor: 24, AllowFraction: false, Price1: 5, Active: true}
	if err := db.Create(&caja).Error; err != nil {
		t.Fatal(err)
	}
	pid, cajaID := p.ID, caja.ID

	if _, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, SaleUnitID: &cajaID, Description: "Producto en caja", Unit: "NIU", Quantity: 1, UnitCost: 100,
		IgvAffectationType: "10",
	}); err != nil {
		t.Fatal(err)
	}
	var mv database.TenantStockMovement
	db.Where("product_id = ?", p.ID).First(&mv)
	want := 100.0 / 24.0 // 4.1666666...
	if math.Abs(mv.UnitCost-want) > 1e-6 {
		t.Errorf("UnitCost = %.6f, want ~%.6f (100/24, sin truncar a 4.17)", mv.UnitCost, want)
	}
	if math.Abs(mv.UnitCost-4.17) < 1e-6 {
		t.Error("el costo se truncó a 2 decimales (4.17) — decimal(15,6) debería preservar más precisión")
	}
}

