package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupPurchaseServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantProduct{},
		&database.TenantPurchase{},
		&database.TenantPurchaseItem{},
		&database.TenantProductStock{},
		&database.TenantStockMovement{},
		&database.TenantInventoryOperationType{},
		&database.TenantProductSerial{},
		&database.TenantBankAccount{},
		&database.TenantBankMovement{},
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

func TestValidatePurchaseItems_RejectsZeroNewSalePrice(t *testing.T) {
	err := validatePurchaseItems([]PurchaseItemInput{
		{Description: "Item A", UpdateSalePrice: true, NewSalePrice: 0},
	})
	if err == nil {
		t.Fatal("expected error for new_sale_price <= 0 with update_sale_price")
	}
	if !strings.Contains(err.Error(), "mayor a cero") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePurchaseItems_RejectsNegativeNewSalePrice(t *testing.T) {
	err := validatePurchaseItems([]PurchaseItemInput{
		{UpdateSalePrice: true, NewSalePrice: -1},
	})
	if err == nil {
		t.Fatal("expected error for negative new_sale_price")
	}
}

func TestCatalogPriceUpdates_SkipsPurchasePriceWhenUnitCostNotPositive(t *testing.T) {
	for _, cost := range []float64{0, -5} {
		updates, err := catalogPriceUpdates(PurchaseItemInput{
			UnitCost:        cost,
			UpdateSalePrice: false,
		})
		if err != nil {
			t.Fatalf("unit_cost=%v: %v", cost, err)
		}
		if updates != nil {
			t.Fatalf("unit_cost=%v: expected nil updates, got %v", cost, updates)
		}
	}
}

func TestCatalogPriceUpdates_IncludesSalePriceWhenUnitCostZero(t *testing.T) {
	updates, err := catalogPriceUpdates(PurchaseItemInput{
		UnitCost:        0,
		UpdateSalePrice: true,
		NewSalePrice:    25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updates == nil {
		t.Fatal("expected sale_price update")
	}
	if _, ok := updates["purchase_price"]; ok {
		t.Fatal("purchase_price must not be updated when unit_cost <= 0")
	}
	if updates["sale_price"] != 25.0 {
		t.Fatalf("sale_price: got %v want 25", updates["sale_price"])
	}
}

func TestCatalogPriceUpdates_RejectsInvalidSalePrice(t *testing.T) {
	_, err := catalogPriceUpdates(PurchaseItemInput{
		UnitCost:        10,
		UpdateSalePrice: true,
		NewSalePrice:    0,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPurchaseCreate_UpdatesCatalogPrices(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	product := &database.TenantProduct{
		Code:               "P001",
		Name:               "Producto test",
		Type:               "product",
		Unit:               "NIU",
		SalePrice:          20,
		PurchasePrice:      5,
		TaxRate:            18,
		IgvAffectationType: "10",
		ManageStock:        true,
		Active:             true,
	}
	if err := db.Create(product).Error; err != nil {
		t.Fatal(err)
	}

	pid := product.ID
	_, err := svc.Create(CreatePurchaseInput{
		BranchID:  1,
		UserID:    1,
		DocType:   "FACTURA",
		Series:    "F001",
		Number:    "123",
		IssueDate: time.Now(),
		Currency:  "PEN",
		Items: []PurchaseItemInput{
			{
				ProductID:          &pid,
				Description:        "Producto test",
				Unit:               "NIU",
				Quantity:           2,
				UnitCost:           8.5,
				IgvAffectationType: "10",
				UpdateSalePrice:    true,
				NewSalePrice:       15.99,
			},
		},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var loaded database.TenantProduct
	if err := db.First(&loaded, product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.PurchasePrice != 8.5 {
		t.Fatalf("purchase_price: got %.2f want 8.50", loaded.PurchasePrice)
	}
	if loaded.SalePrice != 15.99 {
		t.Fatalf("sale_price: got %.2f want 15.99", loaded.SalePrice)
	}
}

func TestPurchaseCreate_UpdatesPurchasePriceOnlyWhenSaleFlagOff(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	product := &database.TenantProduct{
		Code:               "P002",
		Name:               "Sin cambio venta",
		Type:               "product",
		Unit:               "NIU",
		SalePrice:          30,
		PurchasePrice:      10,
		TaxRate:            18,
		IgvAffectationType: "10",
		ManageStock:        false,
		Active:             true,
	}
	if err := db.Create(product).Error; err != nil {
		t.Fatal(err)
	}

	pid := product.ID
	_, err := svc.Create(CreatePurchaseInput{
		BranchID:  1,
		UserID:    1,
		DocType:   "FACTURA",
		Series:    "F001",
		Number:    "124",
		IssueDate: time.Now(),
		Items: []PurchaseItemInput{
			{
				ProductID:          &pid,
				Description:        "Sin cambio venta",
				Quantity:           1,
				UnitCost:           12,
				IgvAffectationType: "10",
				UpdateSalePrice:    false,
			},
		},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var loaded database.TenantProduct
	if err := db.First(&loaded, product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.PurchasePrice != 12 {
		t.Fatalf("purchase_price: got %.2f want 12", loaded.PurchasePrice)
	}
	if loaded.SalePrice != 30 {
		t.Fatalf("sale_price should remain 30, got %.2f", loaded.SalePrice)
	}
}

func TestPurchaseCreate_SkipsPurchasePriceWhenUnitCostZero(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	product := &database.TenantProduct{
		Code:               "P003",
		Name:               "Bonificación",
		Type:               "product",
		Unit:               "NIU",
		SalePrice:          18,
		PurchasePrice:      9,
		TaxRate:            18,
		IgvAffectationType: "10",
		ManageStock:        false,
		Active:             true,
	}
	if err := db.Create(product).Error; err != nil {
		t.Fatal(err)
	}

	pid := product.ID
	_, err := svc.Create(CreatePurchaseInput{
		BranchID:  1,
		UserID:    1,
		DocType:   "FACTURA",
		Series:    "F001",
		Number:    "125",
		IssueDate: time.Now(),
		Items: []PurchaseItemInput{
			{
				ProductID:          &pid,
				Description:        "Bonificación",
				Quantity:           5,
				UnitCost:           0,
				IgvAffectationType: "10",
			},
		},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var loaded database.TenantProduct
	if err := db.First(&loaded, product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.PurchasePrice != 9 {
		t.Fatalf("purchase_price must remain 9 on zero-cost line, got %.2f", loaded.PurchasePrice)
	}
}

func TestPurchaseCreate_RejectsInvalidSalePriceBeforePersist(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	product := &database.TenantProduct{
		Code: "P004", Name: "Validación", Type: "product", Unit: "NIU",
		SalePrice: 10, PurchasePrice: 5, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: false, Active: true,
	}
	if err := db.Create(product).Error; err != nil {
		t.Fatal(err)
	}
	pid := product.ID

	var before int64
	db.Model(&database.TenantPurchase{}).Count(&before)

	_, err := svc.Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "126",
		IssueDate: time.Now(),
		Items: []PurchaseItemInput{{
			ProductID: &pid, Description: "Validación", Quantity: 1, UnitCost: 8,
			IgvAffectationType: "10", UpdateSalePrice: true, NewSalePrice: 0,
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}

	var after int64
	db.Model(&database.TenantPurchase{}).Count(&after)
	if after != before {
		t.Fatalf("purchase rows: before=%d after=%d, want no insert on validation failure", before, after)
	}

	var loaded database.TenantProduct
	if err := db.First(&loaded, product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.PurchasePrice != 5 || loaded.SalePrice != 10 {
		t.Fatalf("catalog prices changed on failed create: purchase=%.2f sale=%.2f", loaded.PurchasePrice, loaded.SalePrice)
	}
}

func TestPurchaseCreate_BankMovementInSameTransaction(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	acc := &database.TenantBankAccount{
		Name:          "Efectivo compras",
		PaymentMethod: "efectivo",
		Balance:       1000,
		Type:          "cash",
		Active:        true,
	}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}

	product := &database.TenantProduct{
		Code: "P-BANK", Name: "Con pago", Type: "product", Unit: "NIU",
		SalePrice: 10, PurchasePrice: 5, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: false, Active: true,
	}
	if err := db.Create(product).Error; err != nil {
		t.Fatal(err)
	}
	pid := product.ID

	_, err := svc.Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "200",
		IssueDate: time.Now(), PaymentMethod: "efectivo",
		Items: []PurchaseItemInput{{
			ProductID: &pid, Description: "Con pago", Quantity: 1, UnitCost: 100,
			IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var movCount int64
	db.Model(&database.TenantBankMovement{}).Count(&movCount)
	if movCount != 1 {
		t.Fatalf("bank movements: got %d want 1", movCount)
	}

	var loadedAcc database.TenantBankAccount
	if err := db.First(&loadedAcc, acc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loadedAcc.Balance != 882 {
		t.Fatalf("balance: got %.2f want 882 (total compra 118 con IGV)", loadedAcc.Balance)
	}
}

func TestPurchaseCreate_RollbackBankMovementOnCatalogPriceError(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	acc := &database.TenantBankAccount{
		Name: "Efectivo", PaymentMethod: "efectivo", Balance: 500, Type: "cash", Active: true,
	}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}

	p1 := &database.TenantProduct{
		Code: "P-OK", Name: "OK", Type: "product", Unit: "NIU",
		SalePrice: 10, PurchasePrice: 5, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: false, Active: true,
	}
	if err := db.Create(p1).Error; err != nil {
		t.Fatal(err)
	}
	pid1 := p1.ID

	var beforePurchases, beforeMovs int64
	db.Model(&database.TenantPurchase{}).Count(&beforePurchases)
	db.Model(&database.TenantBankMovement{}).Count(&beforeMovs)

	_, err := svc.Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "201",
		IssueDate: time.Now(), PaymentMethod: "efectivo",
		Items: []PurchaseItemInput{
			{ProductID: &pid1, Description: "OK", Quantity: 1, UnitCost: 50, IgvAffectationType: "10"},
			{Description: "Sin producto", Quantity: 1, UnitCost: 10, IgvAffectationType: "10",
				UpdateSalePrice: true, NewSalePrice: 0},
		},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}

	var afterPurchases, afterMovs int64
	db.Model(&database.TenantPurchase{}).Count(&afterPurchases)
	db.Model(&database.TenantBankMovement{}).Count(&afterMovs)
	if afterPurchases != beforePurchases || afterMovs != beforeMovs {
		t.Fatalf("rollback failed: purchases %d->%d movements %d->%d",
			beforePurchases, afterPurchases, beforeMovs, afterMovs)
	}

	var loadedAcc database.TenantBankAccount
	if err := db.First(&loadedAcc, acc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loadedAcc.Balance != 500 {
		t.Fatalf("balance changed on failed create: got %.2f", loadedAcc.Balance)
	}
}

func TestPurchaseCreate_RejectsMissingProduct(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	missingID := uint(40404)
	_, err := svc.Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "404",
		IssueDate: time.Now(),
		Items: []PurchaseItemInput{{
			ProductID: &missingID, Description: "Fantasma", Quantity: 1, UnitCost: 10,
			IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err == nil {
		t.Fatal("expected error for missing product")
	}
	if !strings.Contains(err.Error(), "ya no existe o fue eliminado") {
		t.Fatalf("unexpected error: %v", err)
	}

	var count int64
	db.Model(&database.TenantPurchase{}).Count(&count)
	if count != 0 {
		t.Fatalf("purchase persisted despite invalid product")
	}
}

func TestPurchaseVoid_ReversesBankDebit(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	acc := &database.TenantBankAccount{
		Name: "Efectivo", PaymentMethod: "efectivo", Balance: 1000, Type: "cash", Active: true,
	}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}
	product := &database.TenantProduct{
		Code: "P-VOID", Name: "Anulable", Type: "product", Unit: "NIU",
		SalePrice: 10, PurchasePrice: 5, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: false, Active: true,
	}
	if err := db.Create(product).Error; err != nil {
		t.Fatal(err)
	}
	pid := product.ID

	purchase, err := svc.Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "300",
		IssueDate: time.Now(), PaymentMethod: "efectivo",
		Items: []PurchaseItemInput{{
			ProductID: &pid, Description: "Anulable", Quantity: 1, UnitCost: 100,
			IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var debit database.TenantBankMovement
	if err := db.Where("reference = ? AND type = ?", "F001-300", "debit").First(&debit).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.Void(purchase.ID, 1); err != nil {
		t.Fatalf("Void: %v", err)
	}

	var rev database.TenantBankMovement
	if err := db.Where("reversal_of_id = ?", debit.ID).First(&rev).Error; err != nil {
		t.Fatal(err)
	}
	if rev.Type != "credit" {
		t.Fatalf("reversal type: %s", rev.Type)
	}
	if rev.Description != "Reversión por anulación de compra" {
		t.Fatalf("description: %s", rev.Description)
	}

	var loadedAcc database.TenantBankAccount
	if err := db.First(&loadedAcc, acc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loadedAcc.Balance != 1000 {
		t.Fatalf("balance after void: got %.2f want 1000", loadedAcc.Balance)
	}
}

func TestPurchaseCreate_RollbackWhenCatalogPriceUpdateFails(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	product := &database.TenantProduct{
		Code: "P005", Name: "Rollback", Type: "product", Unit: "NIU",
		SalePrice: 10, PurchasePrice: 5, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: true, Active: true,
	}
	if err := db.Create(product).Error; err != nil {
		t.Fatal(err)
	}
	pid := product.ID

	// Forzar fallo en Updates: tabla renombrada dentro de la transacción vía callback no es trivial;
	// simulamos con product_id inexistente en un segundo ítem y validación que falla en el mismo Create.
	missingID := uint(99999)
	var beforePurchases, beforeItems int64
	db.Model(&database.TenantPurchase{}).Count(&beforePurchases)
	db.Model(&database.TenantPurchaseItem{}).Count(&beforeItems)

	_, err := svc.Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "127",
		IssueDate: time.Now(),
		Items: []PurchaseItemInput{
			{
				ProductID: &pid, Description: "OK", Quantity: 1, UnitCost: 6,
				IgvAffectationType: "10", UpdateSalePrice: true, NewSalePrice: 12,
			},
			{
				ProductID: &missingID, Description: "Fantasma", Quantity: 1, UnitCost: 1,
				IgvAffectationType: "10", UpdateSalePrice: true, NewSalePrice: 0,
			},
		},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err == nil {
		t.Fatal("expected error from second item sale price validation")
	}

	var afterPurchases, afterItems int64
	db.Model(&database.TenantPurchase{}).Count(&afterPurchases)
	db.Model(&database.TenantPurchaseItem{}).Count(&afterItems)
	if afterPurchases != beforePurchases || afterItems != beforeItems {
		t.Fatalf("transaction not rolled back: purchases %d->%d items %d->%d",
			beforePurchases, afterPurchases, beforeItems, afterItems)
	}

	var loaded database.TenantProduct
	if err := db.First(&loaded, product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.PurchasePrice != 5 || loaded.SalePrice != 10 {
		t.Fatalf("first product prices changed after rollback: purchase=%.2f sale=%.2f",
			loaded.PurchasePrice, loaded.SalePrice)
	}
}

// createPurchaseWithIgvFlag registra una compra de 1 unidad a 118 con la afectación dada.
func createPurchaseWithIgvFlag(t *testing.T, affectation string, priceIncludesIgv bool) *database.TenantPurchase {
	t.Helper()
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	p, err := svc.Create(CreatePurchaseInput{
		BranchID:         1,
		UserID:           1,
		DocType:          "FACTURA",
		Series:           "F001",
		Number:           "1",
		IssueDate:        time.Now(),
		Currency:         "PEN",
		PriceIncludesIgv: priceIncludesIgv,
		Items: []PurchaseItemInput{
			{
				Description:        "Item",
				Unit:               "NIU",
				Quantity:           1,
				UnitCost:           118,
				IgvAffectationType: affectation,
				PriceIncludesIgv:   priceIncludesIgv,
			},
		},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return p
}

// Con el check marcado el costo ya trae IGV: 118 se desagrega en 100 + 18.
func TestPurchaseCreate_PriceIncludesIgvDecomposes(t *testing.T) {
	p := createPurchaseWithIgvFlag(t, "10", true)
	if p.Subtotal != 100 {
		t.Fatalf("subtotal: got %.2f want 100.00", p.Subtotal)
	}
	if p.TaxAmount != 18 {
		t.Fatalf("tax_amount: got %.2f want 18.00", p.TaxAmount)
	}
	if p.Total != 118 {
		t.Fatalf("total: got %.2f want 118.00", p.Total)
	}
	if !p.PriceIncludesIgv {
		t.Fatal("price_includes_igv debe persistirse como true")
	}
}

// Sin el check el IGV se suma encima: 118 pasa a 118 + 21.24.
func TestPurchaseCreate_PriceExcludesIgvAddsOnTop(t *testing.T) {
	p := createPurchaseWithIgvFlag(t, "10", false)
	if p.Subtotal != 118 {
		t.Fatalf("subtotal: got %.2f want 118.00", p.Subtotal)
	}
	if p.TaxAmount != 21.24 {
		t.Fatalf("tax_amount: got %.2f want 21.24", p.TaxAmount)
	}
	if p.Total != 139.24 {
		t.Fatalf("total: got %.2f want 139.24", p.Total)
	}
	if p.PriceIncludesIgv {
		t.Fatal("price_includes_igv debe persistirse como false")
	}
}

// En exonerados el flag no debe alterar nada: el costo se respeta tal cual.
func TestPurchaseCreate_PriceIncludesIgvIgnoredWhenExonerated(t *testing.T) {
	for _, includes := range []bool{true, false} {
		p := createPurchaseWithIgvFlag(t, "20", includes)
		if p.Subtotal != 118 || p.TaxAmount != 0 || p.Total != 118 {
			t.Fatalf("exonerado con includes=%v: got %.2f/%.2f/%.2f want 118.00/0.00/118.00",
				includes, p.Subtotal, p.TaxAmount, p.Total)
		}
	}
}
