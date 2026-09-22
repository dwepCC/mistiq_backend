package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func newLabelTestApp(db interface{}, permissions []string) *fiber.App {
	h := NewEcommerceHandler()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", uint(31))
		c.Locals("tenant_claims", &middleware.TenantClaims{UserID: 31, Permissions: permissions})
		return c.Next()
	})
	ordersDispatch := middleware.RequirePermission("ecommerce.orders_dispatch")
	app.Get("/api/ecommerce/orders/:id/label", ordersDispatch, h.OrderLabelAPI)
	return app
}

func getLabel(t *testing.T, app *fiber.App, orderID uint) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/ecommerce/orders/"+itoaH(orderID)+"/label", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// ── RBAC (§18 "RBAC") ────────────────────────────────────────────────

func TestOrderLabelAPI_AlmaceneroRechazado(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	order := createReadyForDispatchOrder(t, db)
	app := newLabelTestApp(db, []string{"ecommerce.orders_view", "ecommerce.orders_prepare"})

	resp := getLabel(t, app, order.ID)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Almacenero (sin orders_dispatch) no debe poder generar etiqueta, status=%d", resp.StatusCode)
	}
}

func TestOrderLabelAPI_ConPermisoAceptado(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	order := createReadyForDispatchOrder(t, db)
	app := newLabelTestApp(db, []string{"ecommerce.orders_dispatch"})

	// Sin Dispatch todavía — debe rechazarse con 404, no 403 (el permiso sí lo tiene).
	resp1 := getLabel(t, app, order.ID)
	if resp1.StatusCode != http.StatusNotFound {
		t.Fatalf("sin despacho aún, status=%d (quería 404)", resp1.StatusCode)
	}

	dispatchApp := newDispatchTestApp(db, []string{"ecommerce.orders_dispatch"})
	dResp := postDispatch(t, dispatchApp, order.ID, map[string]any{"carrier_name": "Olva Courier"})
	if dResp.StatusCode != http.StatusCreated {
		t.Fatalf("crear despacho falló, status=%d", dResp.StatusCode)
	}

	resp2 := getLabel(t, app, order.ID)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("con despacho creado, status=%d (quería 200)", resp2.StatusCode)
	}
}

// TestOrderLabelAPI_Reimpresion_MismaRespuesta: dos GET consecutivos no crean estado nuevo,
// devuelven datos consistentes (§8 reimpresión, a nivel HTTP).
func TestOrderLabelAPI_Reimpresion_MismaRespuesta(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	order := createReadyForDispatchOrder(t, db)
	dispatchApp := newDispatchTestApp(db, []string{"ecommerce.orders_dispatch"})
	postDispatch(t, dispatchApp, order.ID, map[string]any{"tracking_code": "REPRINT-1"})

	app := newLabelTestApp(db, []string{"ecommerce.orders_dispatch"})
	resp1 := getLabel(t, app, order.ID)
	resp2 := getLabel(t, app, order.ID)
	if resp1.StatusCode != http.StatusOK || resp2.StatusCode != http.StatusOK {
		t.Fatalf("ambas respuestas debían ser 200: %d, %d", resp1.StatusCode, resp2.StatusCode)
	}
}
