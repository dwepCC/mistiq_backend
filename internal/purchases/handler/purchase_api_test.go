package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tukifac/config"
	"tukifac/internal/purchases/service"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// setupPurchaseAPITestDB sigue el mismo patrón de sqlite en memoria que
// setupProductHandlerTestDB (internal/products/handler) y setupPurchaseSaleUnitDB
// (internal/purchases/service/purchase_sale_unit_integration_test.go).
func setupPurchaseAPITestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantProduct{}, &database.TenantBranch{}, &database.TenantPurchase{}, &database.TenantPurchaseItem{},
		&database.TenantProductStock{}, &database.TenantStockMovement{},
		&database.TenantInventoryOperationType{}, &database.TenantProductSerial{},
		&database.TenantBankAccount{}, &database.TenantBankMovement{}, &database.TenantPaymentMethod{},
		&database.TenantCashSession{}, &database.TenantCashMovement{},
		&database.TenantPurchasePayable{}, &database.TenantPurchasePayment{},
		&database.TenantProductSaleUnit{}, &database.TenantContact{},
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

// newOpenCashSession replica el helper del paquete service (no exportado, no reusable desde
// handler) — una compra siempre exige sesión de caja abierta (PurchaseService.Create).
func newOpenCashSession(t *testing.T, db *gorm.DB, branchID, userID uint) {
	t.Helper()
	if err := db.Create(&database.TenantCashSession{
		BranchID: branchID, UserID: userID, OpenedBy: userID, Status: "open", OpeningBalance: 0,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func newPurchaseTestApp(db *gorm.DB) *fiber.App {
	h := &PurchaseHandler{}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", uint(1))
		c.Locals("active_branch_id", uint(1)) // lo inyecta el middleware de sucursal en producción
		return c.Next()
	})
	app.Get("/purchases/:id", h.GetAPI)
	return app
}

// TestGetAPI_ExposesSaleUnitSnapshot verifica el punto D.1/W.1 del FASE_7H_AUDIT_REPORT.md: una
// línea de compra con SaleUnit debe devolver sale_unit_id/sale_unit_quantity/conversion_factor en
// GET /api/purchases/:id (antes de este cambio, itemRow los omitía aunque ya estaban persistidos
// en TenantPurchaseItem.SaleUnitID y en el TenantStockMovement vinculado). Una línea legacy (sin
// SaleUnit) en la misma compra no debe ganar estos campos de la nada.
func TestGetAPI_ExposesSaleUnitSnapshot(t *testing.T) {
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	db := setupPurchaseAPITestDB(t)
	db.Create(&database.TenantBranch{Name: "Principal", IsMain: true, Active: true})
	newOpenCashSession(t, db, 1, 1)

	saco := database.TenantProduct{
		Code: "ARR-KG", Name: "Arroz Superior", Type: "product", Unit: "KGM",
		SalePrice: 5, ManageStock: true, IgvAffectationType: "10", Active: true,
	}
	if err := db.Create(&saco).Error; err != nil {
		t.Fatal(err)
	}
	saco100 := database.TenantProductSaleUnit{
		ProductID: saco.ID, Name: "Saco 100 KG", ConversionFactor: 100, AllowFraction: true,
		Price1: 4.5, Active: true,
	}
	if err := db.Create(&saco100).Error; err != nil {
		t.Fatal(err)
	}
	legacy := database.TenantProduct{
		Code: "LEG-KG", Name: "Producto legacy", Type: "product", Unit: "KGM",
		SalePrice: 10, ManageStock: true, IgvAffectationType: "10", Active: true,
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}

	sacoID, legacyID, saco100ID := saco.ID, legacy.ID, saco100.ID
	purchase, err := service.NewPurchaseService(db).Create(service.CreatePurchaseInput{
		BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001",
		Number: fmt.Sprintf("%d", time.Now().UnixNano()), IssueDate: time.Now(), Currency: "PEN",
		Items: []service.PurchaseItemInput{
			{ProductID: &sacoID, Description: "Arroz por saco", Unit: "KGM", Quantity: 10, UnitCost: 350, SaleUnitID: &saco100ID, IgvAffectationType: "10"},
			{ProductID: &legacyID, Description: "Producto legacy", Unit: "KGM", Quantity: 5, UnitCost: 3, IgvAffectationType: "10"},
		},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	app := newPurchaseTestApp(db)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/purchases/%d", purchase.ID), nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var payload struct {
		Data struct {
			Items []struct {
				ProductID        *uint    `json:"product_id"`
				SaleUnitID       *uint    `json:"sale_unit_id"`
				SaleUnitQuantity *float64 `json:"sale_unit_quantity"`
				ConversionFactor *float64 `json:"conversion_factor"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Data.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(payload.Data.Items))
	}

	var sacoItem, legacyItem *struct {
		ProductID        *uint    `json:"product_id"`
		SaleUnitID       *uint    `json:"sale_unit_id"`
		SaleUnitQuantity *float64 `json:"sale_unit_quantity"`
		ConversionFactor *float64 `json:"conversion_factor"`
	}
	for i := range payload.Data.Items {
		it := &payload.Data.Items[i]
		if it.ProductID != nil && *it.ProductID == sacoID {
			sacoItem = it
		}
		if it.ProductID != nil && *it.ProductID == legacyID {
			legacyItem = it
		}
	}
	if sacoItem == nil {
		t.Fatal("no se encontró la línea del saco en la respuesta")
	}
	if sacoItem.SaleUnitID == nil || *sacoItem.SaleUnitID != saco100ID {
		t.Errorf("sale_unit_id = %v, want %d", sacoItem.SaleUnitID, saco100ID)
	}
	if sacoItem.SaleUnitQuantity == nil || *sacoItem.SaleUnitQuantity != 10 {
		t.Errorf("sale_unit_quantity = %v, want 10 (cantidad comercial)", sacoItem.SaleUnitQuantity)
	}
	if sacoItem.ConversionFactor == nil || *sacoItem.ConversionFactor != 100 {
		t.Errorf("conversion_factor = %v, want 100", sacoItem.ConversionFactor)
	}

	if legacyItem == nil {
		t.Fatal("no se encontró la línea legacy en la respuesta")
	}
	if legacyItem.SaleUnitID != nil {
		t.Errorf("sale_unit_id de línea legacy = %v, want nil", legacyItem.SaleUnitID)
	}
	if legacyItem.SaleUnitQuantity != nil {
		t.Errorf("sale_unit_quantity de línea legacy = %v, want nil", legacyItem.SaleUnitQuantity)
	}
	if legacyItem.ConversionFactor != nil {
		t.Errorf("conversion_factor de línea legacy = %v, want nil", legacyItem.ConversionFactor)
	}
}
