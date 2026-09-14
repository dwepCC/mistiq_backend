package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// setupProductHandlerTestDB sigue el mismo patrón que el resto del paquete de handlers (sqlite
// en memoria, cache compartido por nombre de test — ver setupCalcTotalsDB en
// internal/cashbank/handler/cashbank_handler_test.go).
func setupProductHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantProduct{},
		&database.TenantUnit{},
		&database.TenantBranch{},
		&database.TenantProductStock{},
		&database.TenantCompanyConfig{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

// newProductTestApp monta ProductHandler.UpdateAPI directo (sin RequireModule/RequirePermission
// de routes.go, que ya tienen su propia cobertura) con un middleware que inyecta los Locals que
// los handlers de productos leen: tenantDB (db(c)), active_branch_id (branch.ActiveBranchID) y
// user_id.
func newProductTestApp(db *gorm.DB, activeBranchID uint) *fiber.App {
	h := &ProductHandler{}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("active_branch_id", activeBranchID)
		c.Locals("user_id", uint(1))
		c.Locals("user_email", "test@tukifac.com")
		return c.Next()
	})
	app.Put("/products/:id", h.UpdateAPI)
	return app
}

// TestUpdateAPI_EnablingManageStockCreatesStockRow reproduce el bug real: tenant industrialrafaz
// (RUC 20614234165), 07-sep-2026 — un producto de comercio general (is_restaurant=false) creado
// SIN control de stock, al editarlo para activar "Control de stock" (manage_stock: false→true)
// sin indicar stock inicial, desaparecía de cualquier listado filtrado por sucursal: nunca se
// creaba su fila en tenant_product_stocks (ProductService.Update solo escribe la columna
// manage_stock, no siembra stock), y applyProductListFilters exige "manage_stock = false OR
// EXISTS fila de stock para esa sucursal" cuando se filtra por sucursal. UpdateAPI sí llamaba
// EnsureProductBranchLink para productos de restaurante (Tukichef) pero no tenía el equivalente
// para comercio general — a diferencia de CreateAPI, que sí lo hace en el alta.
func TestUpdateAPI_EnablingManageStockCreatesStockRow(t *testing.T) {
	db := setupProductHandlerTestDB(t)
	db.Create(&database.TenantBranch{Name: "Principal", IsMain: true, Active: true})

	p := &database.TenantProduct{
		Code:        "ABC123",
		Name:        "Producto sin stock",
		SalePrice:   19.9,
		ManageStock: false,
		BranchID:    0, // "disponible en todas las sucursales" — comercio general, no restaurante.
		Active:      true,
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatal(err)
	}

	app := newProductTestApp(db, 1)

	body, _ := json.Marshal(map[string]interface{}{
		"code":         p.Code,
		"name":         p.Name,
		"sale_price":   p.SalePrice,
		"manage_stock": true,
	})
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/products/%d", p.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var updated database.TenantProduct
	if err := db.First(&updated, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !updated.ManageStock {
		t.Fatal("manage_stock debería haber quedado en true")
	}

	var stock database.TenantProductStock
	err = db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock).Error
	if err != nil {
		t.Fatalf(
			"no se creó la fila en tenant_product_stocks al activar manage_stock (bug real, "+
				"el producto queda invisible en cualquier listado filtrado por sucursal): %v", err,
		)
	}
	if stock.Quantity != 0 {
		t.Errorf("quantity = %v, want 0 (no se indicó stock inicial)", stock.Quantity)
	}
}
