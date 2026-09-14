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
		&database.TenantPaymentMethod{},
		&database.TenantCashSession{},
		&database.TenantCashMovement{},
		&database.TenantPurchasePayable{},
		&database.TenantPurchasePayment{},
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
	newOpenCashSession(t, db, 1, 1) // toda compra exige sesión de caja abierta, aun a crédito

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
	newOpenCashSession(t, db, 1, 1) // toda compra exige sesión de caja abierta, aun a crédito

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
	newOpenCashSession(t, db, 1, 1) // toda compra exige sesión de caja abierta, aun a crédito

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

// Método bancario (no efectivo) a propósito: desde la Fase 3, "efectivo" exige sesión de caja
// abierta y va a tenant_cash_movements (ver TestPurchaseCreate_CashRequiresOpenSession /
// TestPurchaseCreate_CashCreatesCashMovement). Este test sigue cubriendo el caso original —
// compra por un método que va a cuenta bancaria, sin sesión de por medio.
func TestPurchaseCreate_BankMovementInSameTransaction(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	acc := &database.TenantBankAccount{
		Name:          "Cta transferencias",
		PaymentMethod: "transferencia",
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

	// Una compra no-efectivo también exige sesión de caja abierta del usuario — igual que una
	// venta, y por el mismo motivo: quedar vinculada de forma determinística, no por
	// sucursal+fecha. No genera ningún TenantCashMovement (el pago fue por transferencia).
	session := newOpenCashSession(t, db, 1, 1)

	purchase, err := svc.Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "200",
		IssueDate: time.Now(), PaymentMethod: "transferencia",
		Items: []PurchaseItemInput{{
			ProductID: &pid, Description: "Con pago", Quantity: 1, UnitCost: 100,
			IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if purchase.CashSessionID == nil || *purchase.CashSessionID != session.ID {
		t.Fatalf("purchase.CashSessionID = %v, want %d", purchase.CashSessionID, session.ID)
	}

	var movCount int64
	db.Model(&database.TenantBankMovement{}).Count(&movCount)
	if movCount != 1 {
		t.Fatalf("bank movements: got %d want 1", movCount)
	}
	var bankMov database.TenantBankMovement
	if err := db.Where("purchase_id = ?", purchase.ID).First(&bankMov).Error; err != nil {
		t.Fatal(err)
	}
	if bankMov.CashSessionID == nil || *bankMov.CashSessionID != session.ID {
		t.Errorf("bank_movement.cash_session_id = %v, want %d", bankMov.CashSessionID, session.ID)
	}
	var cashMovCount int64
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", purchase.ID).Count(&cashMovCount)
	if cashMovCount != 0 {
		t.Errorf("no debe crearse ningún TenantCashMovement para una compra por transferencia, got %d", cashMovCount)
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

// Método bancario a propósito, mismo motivo que TestPurchaseCreate_BankMovementInSameTransaction
// — la reversión de una compra en efectivo real se cubre en
// TestPurchaseVoid_ReversesCashExpense.
func TestPurchaseVoid_ReversesBankDebit(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	acc := &database.TenantBankAccount{
		Name: "Cta transferencias", PaymentMethod: "transferencia", Balance: 1000, Type: "bank", Active: true,
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
	newOpenCashSession(t, db, 1, 1)

	purchase, err := svc.Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "300",
		IssueDate: time.Now(), PaymentMethod: "transferencia",
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
	newOpenCashSession(t, db, 1, 1) // toda compra exige sesión de caja abierta, aun a crédito

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

// ==== Fase 3: compras en efectivo — sesión de caja real, no cuenta bancaria fake ====

func newOpenCashSession(t *testing.T, db *gorm.DB, branchID, userID uint) *database.TenantCashSession {
	t.Helper()
	sess := &database.TenantCashSession{
		BranchID: branchID, UserID: userID, OpenedBy: userID, Status: "open", OpeningBalance: 0,
	}
	if err := db.Create(sess).Error; err != nil {
		t.Fatal(err)
	}
	return sess
}

// seedCashPaymentMethod siembra el método "cash" (is_system, destination_type=cash) tal como lo
// hace SeedPaymentMethodsCatalog en un tenant real — sin esto, RecordPayment/RecordExpensePayment
// no encuentran el TenantPaymentMethod por código y caen al fallback legado (que busca una
// TenantBankAccount por texto, no crea ningún TenantCashMovement).
func seedCashPaymentMethod(t *testing.T, db *gorm.DB) {
	t.Helper()
	pm := &database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true, DestinationType: "cash"}
	if err := db.Create(pm).Error; err != nil {
		t.Fatal(err)
	}
}

// Sin sesión de caja abierta, una compra en efectivo debe rechazarse — igual que una venta,
// no hay gaveta física de donde salga la plata.
func TestPurchaseCreate_CashRequiresOpenSession(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)

	product := &database.TenantProduct{
		Code: "P-CASH-NOSESSION", Name: "Efectivo sin caja", Type: "product", Unit: "NIU",
		SalePrice: 10, PurchasePrice: 5, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: false, Active: true,
	}
	if err := db.Create(product).Error; err != nil {
		t.Fatal(err)
	}
	pid := product.ID

	_, err := svc.Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "400",
		IssueDate: time.Now(), PaymentMethod: "efectivo",
		Items: []PurchaseItemInput{{
			ProductID: &pid, Description: "Efectivo sin caja", Quantity: 1, UnitCost: 100,
			IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err == nil {
		t.Fatal("esperaba error: compra en efectivo sin sesión de caja abierta")
	}
}

// Con sesión de caja abierta, una compra en efectivo debe:
//  1. Quedar con purchase.cash_session_id poblado.
//  2. Crear un TenantCashMovement (expense, categoría "Compra") en esa sesión — NO un
//     TenantBankMovement.
func TestPurchaseCreate_CashCreatesCashMovementInSession(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	sess := newOpenCashSession(t, db, 1, 1)
	seedCashPaymentMethod(t, db)

	product := &database.TenantProduct{
		Code: "P-CASH-OK", Name: "Efectivo con caja", Type: "product", Unit: "NIU",
		SalePrice: 10, PurchasePrice: 5, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: false, Active: true,
	}
	if err := db.Create(product).Error; err != nil {
		t.Fatal(err)
	}
	pid := product.ID

	purchase, err := svc.Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "401",
		IssueDate: time.Now(), PaymentMethod: "efectivo",
		Items: []PurchaseItemInput{{
			ProductID: &pid, Description: "Efectivo con caja", Quantity: 1, UnitCost: 100,
			IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if purchase.CashSessionID == nil || *purchase.CashSessionID != sess.ID {
		t.Fatalf("purchase.cash_session_id = %v, want %d", purchase.CashSessionID, sess.ID)
	}

	var cashCount, bankCount int64
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", purchase.ID).Count(&cashCount)
	db.Model(&database.TenantBankMovement{}).Where("purchase_id = ?", purchase.ID).Count(&bankCount)
	if cashCount != 1 {
		t.Fatalf("cash movements: got %d want 1", cashCount)
	}
	if bankCount != 0 {
		t.Fatalf("bank movements: got %d want 0 (efectivo no debe tocar tenant_bank_movements)", bankCount)
	}

	var cm database.TenantCashMovement
	if err := db.Where("purchase_id = ?", purchase.ID).First(&cm).Error; err != nil {
		t.Fatal(err)
	}
	if cm.Type != "expense" || cm.Category != "Compra" || cm.CashSessionID != sess.ID {
		t.Fatalf("movimiento inesperado: %+v", cm)
	}
	if cm.Amount != 118 { // 100 + 18% IGV
		t.Fatalf("amount: got %.2f want 118.00", cm.Amount)
	}
}

// Anular una compra en efectivo (sesión aún abierta) debe revertir el egreso con
// reversal_of_id apuntando al movimiento original — sin borrarlo.
func TestPurchaseVoid_ReversesCashExpense(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	newOpenCashSession(t, db, 1, 1)
	seedCashPaymentMethod(t, db)

	product := &database.TenantProduct{
		Code: "P-CASH-VOID", Name: "Anulable efectivo", Type: "product", Unit: "NIU",
		SalePrice: 10, PurchasePrice: 5, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: false, Active: true,
	}
	if err := db.Create(product).Error; err != nil {
		t.Fatal(err)
	}
	pid := product.ID

	purchase, err := svc.Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "402",
		IssueDate: time.Now(), PaymentMethod: "efectivo",
		Items: []PurchaseItemInput{{
			ProductID: &pid, Description: "Anulable efectivo", Quantity: 1, UnitCost: 100,
			IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var original database.TenantCashMovement
	if err := db.Where("purchase_id = ? AND type = ?", purchase.ID, "expense").First(&original).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.Void(purchase.ID, 1); err != nil {
		t.Fatalf("Void: %v", err)
	}

	var rev database.TenantCashMovement
	if err := db.Where("reversal_of_id = ?", original.ID).First(&rev).Error; err != nil {
		t.Fatal(err)
	}
	if rev.Type != "income" || rev.Amount != original.Amount || rev.CashSessionID != original.CashSessionID {
		t.Fatalf("reversión inesperada: %+v (original: %+v)", rev, original)
	}

	// El original sigue intacto — la anulación no borra ni modifica el movimiento original,
	// solo agrega uno compensatorio (trazabilidad, no destrucción de historial).
	var stillThere database.TenantCashMovement
	if err := db.First(&stillThere, original.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stillThere.Type != "expense" || stillThere.Amount != original.Amount {
		t.Fatalf("el movimiento original fue alterado: %+v", stillThere)
	}
}

// Si la sesión donde se pagó en efectivo ya cerró, anular la compra debe fallar con un error
// claro en vez de anular la compra silenciosamente sin revertir el efectivo.
func TestPurchaseVoid_CashSessionClosedReturnsError(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	sess := newOpenCashSession(t, db, 1, 1)
	seedCashPaymentMethod(t, db)

	product := &database.TenantProduct{
		Code: "P-CASH-CLOSED", Name: "Sesión cerrada", Type: "product", Unit: "NIU",
		SalePrice: 10, PurchasePrice: 5, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: false, Active: true,
	}
	if err := db.Create(product).Error; err != nil {
		t.Fatal(err)
	}
	pid := product.ID

	purchase, err := svc.Create(CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: "403",
		IssueDate: time.Now(), PaymentMethod: "efectivo",
		Items: []PurchaseItemInput{{
			ProductID: &pid, Description: "Sesión cerrada", Quantity: 1, UnitCost: 100,
			IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := db.Model(&database.TenantCashSession{}).Where("id = ?", sess.ID).Update("status", "closed").Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.Void(purchase.ID, 1); err == nil {
		t.Fatal("esperaba error: no se puede revertir efectivo de una sesión ya cerrada")
	}

	var p database.TenantPurchase
	if err := db.First(&p, purchase.ID).Error; err != nil {
		t.Fatal(err)
	}
	if p.Status == StatusCancelled {
		t.Fatal("la compra no debe quedar anulada si la reversión de efectivo falló (transacción debe revertir todo)")
	}
}
