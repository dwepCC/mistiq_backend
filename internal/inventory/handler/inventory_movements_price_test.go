package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantmigrations"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// setupInventoryMovementsTestDB sigue el mismo patrón que setupKardexTestDB
// (internal/inventory/service/inventory_kardex_test.go): ApplyBaselineSchema ya crea
// TenantStockMovement con su forma ACTUAL (incluye SaleUnitID/SaleUnitQuantity/ConversionFactor,
// agregados en V135/V136), así que no hace falta replayear cada migración versionada una por una.
func setupInventoryMovementsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := (tenantmigrations.V083InventoryIngressEgress{}).Up(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func newInventoryTestApp(db *gorm.DB, activeBranchID uint) *fiber.App {
	h := &InventoryHandler{}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("active_branch_id", activeBranchID)
		c.Locals("user_id", uint(1))
		return c.Next()
	})
	app.Get("/inventory/movements", h.MovementsAPI)
	return app
}

// TestMovementsAPI_ExposesSaleUnitSnapshot verifica el punto D.1/W.2 del FASE_7H_AUDIT_REPORT.md:
// un movimiento de Kardex que nació de una línea con SaleUnit debe devolver sale_unit_id/
// sale_unit_quantity/conversion_factor en GET /api/inventory/movements (antes de este cambio,
// movementRow los omitía aunque TenantStockMovement ya los persiste desde V135/V136 — ver el
// mismo snapshot histórico que ya expone GET /api/purchases/:id, commit c4650e1). Un movimiento
// legacy sin SaleUnit no debe ganar estos campos de la nada.
func TestMovementsAPI_ExposesSaleUnitSnapshot(t *testing.T) {
	db := setupInventoryMovementsTestDB(t)
	branch := database.TenantBranch{Name: "Principal", Active: true, IsMain: true}
	if err := db.Create(&branch).Error; err != nil {
		t.Fatal(err)
	}

	saco := database.TenantProduct{
		Code: "ARR-KG", Name: "Arroz Superior", Type: "product", Unit: "KGM",
		SalePrice: 5, ManageStock: true, Active: true,
	}
	if err := db.Create(&saco).Error; err != nil {
		t.Fatal(err)
	}
	legacy := database.TenantProduct{
		Code: "LEG-KG", Name: "Producto legacy", Type: "product", Unit: "KGM",
		SalePrice: 10, ManageStock: true, Active: true,
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}

	saleUnitID := uint(42)
	saleUnitQty := 10.0
	factor := 100.0
	movWithSaleUnit := database.TenantStockMovement{
		ProductID: saco.ID, BranchID: branch.ID, Type: "in", Quantity: 1000, UnitCost: 3.5, Balance: 1000,
		SaleUnitID: &saleUnitID, SaleUnitQuantity: &saleUnitQty, ConversionFactor: &factor,
	}
	if err := db.Create(&movWithSaleUnit).Error; err != nil {
		t.Fatal(err)
	}
	movLegacy := database.TenantStockMovement{
		ProductID: legacy.ID, BranchID: branch.ID, Type: "in", Quantity: 5, UnitCost: 3, Balance: 5,
	}
	if err := db.Create(&movLegacy).Error; err != nil {
		t.Fatal(err)
	}

	app := newInventoryTestApp(db, branch.ID)
	req := httptest.NewRequest(http.MethodGet, "/inventory/movements", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var payload struct {
		Data []struct {
			ProductID        uint     `json:"product_id"`
			SaleUnitID       *uint    `json:"sale_unit_id"`
			SaleUnitQuantity *float64 `json:"sale_unit_quantity"`
			ConversionFactor *float64 `json:"conversion_factor"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("movimientos = %d, want 2", len(payload.Data))
	}

	var sacoRow, legacyRow *struct {
		ProductID        uint     `json:"product_id"`
		SaleUnitID       *uint    `json:"sale_unit_id"`
		SaleUnitQuantity *float64 `json:"sale_unit_quantity"`
		ConversionFactor *float64 `json:"conversion_factor"`
	}
	for i := range payload.Data {
		row := &payload.Data[i]
		if row.ProductID == saco.ID {
			sacoRow = row
		}
		if row.ProductID == legacy.ID {
			legacyRow = row
		}
	}
	if sacoRow == nil {
		t.Fatal("no se encontró el movimiento con SaleUnit en la respuesta")
	}
	if sacoRow.SaleUnitID == nil || *sacoRow.SaleUnitID != saleUnitID {
		t.Errorf("sale_unit_id = %v, want %d", sacoRow.SaleUnitID, saleUnitID)
	}
	if sacoRow.SaleUnitQuantity == nil || *sacoRow.SaleUnitQuantity != saleUnitQty {
		t.Errorf("sale_unit_quantity = %v, want %g (cantidad comercial)", sacoRow.SaleUnitQuantity, saleUnitQty)
	}
	if sacoRow.ConversionFactor == nil || *sacoRow.ConversionFactor != factor {
		t.Errorf("conversion_factor = %v, want %g", sacoRow.ConversionFactor, factor)
	}

	if legacyRow == nil {
		t.Fatal("no se encontró el movimiento legacy en la respuesta")
	}
	if legacyRow.SaleUnitID != nil {
		t.Errorf("sale_unit_id de movimiento legacy = %v, want nil", legacyRow.SaleUnitID)
	}
	if legacyRow.SaleUnitQuantity != nil {
		t.Errorf("sale_unit_quantity de movimiento legacy = %v, want nil", legacyRow.SaleUnitQuantity)
	}
	if legacyRow.ConversionFactor != nil {
		t.Errorf("conversion_factor de movimiento legacy = %v, want nil", legacyRow.ConversionFactor)
	}
}
