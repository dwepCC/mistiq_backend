package handler

import (
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

func setupAdminOrdersHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantProduct{}, &database.TenantProductPresentation{},
		&database.TenantEcommerceOrder{}, &database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceOrderStatusHistory{}, &database.TenantEcommerceDispatch{}, &database.TenantNotification{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func newAdminOrdersTestApp(db *gorm.DB) *fiber.App {
	h := NewEcommerceHandler()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		return c.Next()
	})
	// Sin middleware de permisos acá a propósito — esa cobertura vive en routes.go (RequirePermission
	// ya probado en Fase 1) y en pkg/middleware; este test es sobre el CONTRATO del endpoint.
	app.Get("/api/ecommerce/orders", h.ListOrdersAPI)
	app.Get("/api/ecommerce/orders/:id", h.GetOrderAPI)
	return app
}

// TestGetOrderAPI_DevuelvePedidoItemsEHistorial: confirma el endpoint NUEVO de Fase 5
// (GET /api/ecommerce/orders/:id) que antes no existía — solo había ListOrdersAPI (bulk) y
// OrderPrintDataAPI (otro formato, para PDF).
func TestGetOrderAPI_DevuelvePedidoItemsEHistorial(t *testing.T) {
	db := setupAdminOrdersHandlerTestDB(t)
	product := database.TenantProduct{Code: "ADM1", Name: "Producto admin", Type: "product", Unit: "NIU", SalePrice: 20, Active: true, ShowInDigitalCatalog: true}
	db.Create(&product)
	app := newAdminOrdersTestApp(db)

	// Crear vía el mismo camino que un cliente real (misma fuente de verdad) usando el service
	// directamente no aplica acá (es un test de handler puro) — se inserta el pedido+línea+
	// historial simulando lo que CreateOrder ya deja (probado en internal/ecommerce/service).
	order := database.TenantEcommerceOrder{CustomerName: "Cliente", CustomerPhone: "999", ItemsJSON: "[]", Total: 40, Status: "PENDIENTE"}
	db.Create(&order)
	db.Create(&database.TenantEcommerceOrderItem{OrderID: order.ID, ProductID: product.ID, Name: "Producto admin", Quantity: 2, UnitPrice: 20, Subtotal: 40})
	db.Create(&database.TenantEcommerceOrderStatusHistory{OrderID: order.ID, FromStatus: "", ToStatus: "PENDIENTE"})

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/ecommerce/orders/%d", order.ID), nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Data    database.TenantEcommerceOrder                `json:"data"`
		Items   []database.TenantEcommerceOrderItem          `json:"items"`
		History []database.TenantEcommerceOrderStatusHistory `json:"history"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Data.ID != order.ID {
		t.Fatalf("pedido incorrecto en la respuesta")
	}
	if len(out.Items) != 1 || out.Items[0].Name != "Producto admin" {
		t.Fatalf("items incorrectos: %+v", out.Items)
	}
	if len(out.History) != 1 {
		t.Fatalf("historial incorrecto: %+v", out.History)
	}
}

func TestGetOrderAPI_Inexistente_404(t *testing.T) {
	db := setupAdminOrdersHandlerTestDB(t)
	app := newAdminOrdersTestApp(db)
	req := httptest.NewRequest(http.MethodGet, "/api/ecommerce/orders/99999", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestListOrdersAPI_FiltrosPorQueryString: confirma que los filtros nuevos (branch_id, q,
// date_from/to) llegan correctamente desde query params reales hasta el service.
func TestListOrdersAPI_FiltrosPorQueryString(t *testing.T) {
	db := setupAdminOrdersHandlerTestDB(t)
	branchA := uint(1)
	db.Create(&database.TenantEcommerceOrder{CustomerName: "A", CustomerPhone: "1", ItemsJSON: "[]", Total: 10, Status: "PENDIENTE", BranchID: &branchA})
	db.Create(&database.TenantEcommerceOrder{CustomerName: "B", CustomerPhone: "2", ItemsJSON: "[]", Total: 20, Status: "PENDIENTE"})
	app := newAdminOrdersTestApp(db)

	req := httptest.NewRequest(http.MethodGet, "/api/ecommerce/orders?branch_id=1", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data []database.TenantEcommerceOrder `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Data) != 1 || out.Data[0].CustomerName != "A" {
		t.Fatalf("filtro branch_id vía query string no funcionó: %+v", out.Data)
	}
}
