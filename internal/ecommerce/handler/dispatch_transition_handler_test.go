package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"tukifac/internal/ecommerce/service"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

func newDispatchTransitionTestApp(db *gorm.DB, permissions []string) *fiber.App {
	h := NewEcommerceHandler()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", uint(21))
		c.Locals("tenant_claims", &middleware.TenantClaims{UserID: 21, Permissions: permissions})
		return c.Next()
	})
	ordersDispatch := middleware.RequirePermission("ecommerce.orders_dispatch")
	app.Put("/api/ecommerce/dispatches/:id/status", ordersDispatch, h.UpdateDispatchStatusAPI)
	return app
}

// createDispatchedOrderForTransitionTests crea un pedido, lo recorre hasta LISTO_PARA_DESPACHO y
// lo despacha — usa el servicio directo (no HTTP) porque solo nos interesa el endpoint de
// TRANSICIÓN acá, no el de creación (ya cubierto en dispatch_handler_test.go).
func createDispatchedOrderForTransitionTests(t *testing.T, db *gorm.DB) *database.TenantEcommerceDispatch {
	t.Helper()
	product := database.TenantProduct{Code: "DT1", Name: "Producto tracking", Type: "product", Unit: "NIU", SalePrice: 20, Active: true, ShowInDigitalCatalog: true}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	svc := service.NewEcommerceService(db)
	order, _, err := svc.CreateOrder(service.CreateOrderInput{
		CustomerName: "Cliente", CustomerPhone: "999000333", DeliveryMethod: service.DeliveryMethodPickup,
		Items: []service.CreateOrderItemInput{{ProductID: product.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{
		service.OrderStatusConfirmado, service.OrderStatusEnPreparacion,
		service.OrderStatusEmpaquetado, service.OrderStatusListoParaDespacho,
	} {
		if err := svc.UpdateOrderStatus(order.ID, service.UpdateOrderStatusInput{NewStatus: to}); err != nil {
			t.Fatal(err)
		}
	}
	dispatch, err := svc.CreateDispatch(order.ID, service.CreateDispatchInput{})
	if err != nil {
		t.Fatal(err)
	}
	return dispatch
}

func putDispatchStatus(t *testing.T, app *fiber.App, dispatchID uint, status string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"status": status})
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/ecommerce/dispatches/%d/status", dispatchID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// ── RBAC ──────────────────────────────────────────────────────────────

func TestUpdateDispatchStatusAPI_AlmaceneroRechazado(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	dispatch := createDispatchedOrderForTransitionTests(t, db)
	app := newDispatchTransitionTestApp(db, []string{"ecommerce.orders_view", "ecommerce.orders_prepare"})

	resp := putDispatchStatus(t, app, dispatch.ID, "EN_TRANSITO")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Almacenero (sin orders_dispatch) no debe poder marcar EN_TRANSITO, status=%d", resp.StatusCode)
	}
}

func TestUpdateDispatchStatusAPI_VendedorRechazado(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	dispatch := createDispatchedOrderForTransitionTests(t, db)
	app := newDispatchTransitionTestApp(db, []string{"ecommerce.orders_manage", "ecommerce.orders_convert"})

	resp := putDispatchStatus(t, app, dispatch.ID, "EN_TRANSITO")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Vendedor (sin orders_dispatch) no debe poder marcar EN_TRANSITO, status=%d", resp.StatusCode)
	}
}

func TestUpdateDispatchStatusAPI_SupervisorAceptado(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	dispatch := createDispatchedOrderForTransitionTests(t, db)
	app := newDispatchTransitionTestApp(db, []string{"ecommerce.orders_dispatch"})

	resp := putDispatchStatus(t, app, dispatch.ID, "EN_TRANSITO")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Supervisor (orders_dispatch) debe poder marcar EN_TRANSITO, status=%d", resp.StatusCode)
	}
}

func TestUpdateDispatchStatusAPI_EcommerceManageAceptado(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	dispatch := createDispatchedOrderForTransitionTests(t, db)
	app := newDispatchTransitionTestApp(db, []string{"ecommerce.manage"})

	resp := putDispatchStatus(t, app, dispatch.ID, "EN_TRANSITO")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ecommerce.manage debe implicar orders_dispatch, status=%d", resp.StatusCode)
	}
}

// ── Contrato / transiciones ───────────────────────────────────────────

func TestUpdateDispatchStatusAPI_FlujoCompletoViaHTTP(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	dispatch := createDispatchedOrderForTransitionTests(t, db)
	app := newDispatchTransitionTestApp(db, []string{"ecommerce.orders_dispatch"})

	resp1 := putDispatchStatus(t, app, dispatch.ID, "EN_TRANSITO")
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("EN_TRANSITO status=%d", resp1.StatusCode)
	}
	resp2 := putDispatchStatus(t, app, dispatch.ID, "ENTREGADO")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("ENTREGADO status=%d", resp2.StatusCode)
	}
	var out struct {
		Data database.TenantEcommerceDispatch `json:"data"`
	}
	json.NewDecoder(resp2.Body).Decode(&out)
	if out.Data.Status != service.DispatchStatusEntregado || out.Data.DeliveredAt == nil {
		t.Fatalf("respuesta inconsistente tras ENTREGADO: %+v", out.Data)
	}

	var order database.TenantEcommerceOrder
	db.First(&order, out.Data.OrderID)
	if order.Status != service.OrderStatusEntregado {
		t.Fatalf("Order.Status = %q, quería ENTREGADO", order.Status)
	}
}

// TestUpdateDispatchStatusAPI_StatusArbitrario_422: no se acepta cualquier valor de status — solo
// EN_TRANSITO/ENTREGADO son destinos válidos desde este endpoint.
func TestUpdateDispatchStatusAPI_StatusArbitrario_422(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	dispatch := createDispatchedOrderForTransitionTests(t, db)
	app := newDispatchTransitionTestApp(db, []string{"ecommerce.orders_dispatch"})

	resp := putDispatchStatus(t, app, dispatch.ID, "DEVUELTO")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("un status no soportado por este endpoint debe rechazarse con 422, status=%d", resp.StatusCode)
	}
}

// TestUpdateDispatchStatusAPI_SaltarEnTransito_Rechazado: ir directo a ENTREGADO sin pasar por
// EN_TRANSITO debe rechazarse (400), aunque el usuario tenga todos los permisos.
func TestUpdateDispatchStatusAPI_SaltarEnTransito_Rechazado(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	dispatch := createDispatchedOrderForTransitionTests(t, db)
	app := newDispatchTransitionTestApp(db, []string{"ecommerce.manage"})

	resp := putDispatchStatus(t, app, dispatch.ID, "ENTREGADO")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("saltar EN_TRANSITO debe rechazarse (400), status=%d", resp.StatusCode)
	}
}

// TestUpdateDispatchStatusAPI_DobleClick_NuncaRetorna500: doble click real vía HTTP.
func TestUpdateDispatchStatusAPI_DobleClick_NuncaRetorna500(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	dispatch := createDispatchedOrderForTransitionTests(t, db)
	app := newDispatchTransitionTestApp(db, []string{"ecommerce.orders_dispatch"})

	resp1 := putDispatchStatus(t, app, dispatch.ID, "EN_TRANSITO")
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("primer PUT status=%d", resp1.StatusCode)
	}
	resp2 := putDispatchStatus(t, app, dispatch.ID, "EN_TRANSITO")
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("segundo PUT (doble click) debe ser 400, nunca 500, status=%d", resp2.StatusCode)
	}
}

// TestUpdateDispatchAPI_NoAceptaStatusEnElBody: el PATCH de metadata (Fase 8) no tiene (ni debe
// tener) forma de cambiar Status — se prueba enviándolo igual y confirmando que se ignora.
func TestUpdateDispatchAPI_NoAceptaStatusEnElBody(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	dispatch := createDispatchedOrderForTransitionTests(t, db)
	app := newDispatchTestApp(db, []string{"ecommerce.orders_dispatch"})

	body, _ := json.Marshal(map[string]any{"status": "ENTREGADO", "carrier_name": "Shalom"})
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/ecommerce/dispatches/%d", dispatch.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out struct {
		Data database.TenantEcommerceDispatch `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Data.Status != service.DispatchStatusDespachado {
		t.Fatalf("REGRESIÓN: PATCH no debe poder cambiar Status vía el campo \"status\" del body, quedó %q", out.Data.Status)
	}
	if out.Data.CarrierName == nil || *out.Data.CarrierName != "Shalom" {
		t.Fatalf("carrier_name sí debía actualizarse: %+v", out.Data)
	}
}
