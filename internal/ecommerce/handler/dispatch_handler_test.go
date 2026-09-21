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

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var dispatchHandlerTestDBCounter int

func setupDispatchHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dispatchHandlerTestDBCounter++
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), dispatchHandlerTestDBCounter)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantProduct{}, &database.TenantProductPresentation{},
		&database.TenantEcommerceOrder{}, &database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceOrderStatusHistory{}, &database.TenantEcommerceDispatch{},
		&database.TenantNotification{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func newDispatchTestApp(db *gorm.DB, permissions []string) *fiber.App {
	h := NewEcommerceHandler()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", uint(11))
		c.Locals("tenant_claims", &middleware.TenantClaims{UserID: 11, Permissions: permissions})
		return c.Next()
	})
	// Mismo middleware real de routes.go — CreateDispatchAPI/UpdateDispatchAPI, a diferencia de
	// UpdateOrderStatusAPI, no revalidan el permiso adentro del handler (es un permiso fijo, no
	// dinámico por transición), así que la autorización real vive ACÁ, en la ruta.
	ordersDispatch := middleware.RequirePermission("ecommerce.orders_dispatch")
	app.Post("/api/ecommerce/orders/:id/dispatch", ordersDispatch, h.CreateDispatchAPI)
	app.Patch("/api/ecommerce/dispatches/:id", ordersDispatch, h.UpdateDispatchAPI)
	return app
}

// createReadyForDispatchOrder crea un pedido y lo hace avanzar por el flujo REAL de transiciones
// hasta LISTO_PARA_DESPACHO — mismo criterio que createConfirmedOrderForPrepareTests
// (prepare_transition_handler_test.go), un paso más lejos.
func createReadyForDispatchOrder(t *testing.T, db *gorm.DB) *database.TenantEcommerceOrder {
	t.Helper()
	product := database.TenantProduct{Code: "DH1", Name: "Producto despacho", Type: "product", Unit: "NIU", SalePrice: 30, Active: true, ShowInDigitalCatalog: true}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	svc := service.NewEcommerceService(db)
	order, _, err := svc.CreateOrder(service.CreateOrderInput{
		CustomerName: "Cliente", CustomerPhone: "999000222", DeliveryMethod: service.DeliveryMethodPickup,
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
	return order
}

func postDispatch(t *testing.T, app *fiber.App, orderID uint, body map[string]any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/ecommerce/orders/%d/dispatch", orderID), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// ── 6/7/8/9/10. RBAC de despacho ─────────────────────────────────────────

func TestCreateDispatchAPI_AlmaceneroNoPuedeDespachar(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	order := createReadyForDispatchOrder(t, db)
	app := newDispatchTestApp(db, []string{"ecommerce.orders_view", "ecommerce.orders_prepare"})

	resp := postDispatch(t, app, order.ID, map[string]any{})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Almacenero (orders_prepare, sin orders_dispatch) no debe poder despachar, status=%d", resp.StatusCode)
	}
}

func TestCreateDispatchAPI_VendedorNoPuedeDespachar(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	order := createReadyForDispatchOrder(t, db)
	app := newDispatchTestApp(db, []string{"ecommerce.orders_view", "ecommerce.orders_manage", "ecommerce.orders_convert"})

	resp := postDispatch(t, app, order.ID, map[string]any{})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Vendedor (manage+convert, sin orders_dispatch) no debe poder despachar, status=%d", resp.StatusCode)
	}
}

func TestCreateDispatchAPI_SupervisorPuedeDespachar(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	order := createReadyForDispatchOrder(t, db)
	app := newDispatchTestApp(db, []string{
		"ecommerce.orders_view", "ecommerce.orders_manage", "ecommerce.orders_prepare",
		"ecommerce.orders_convert", "ecommerce.orders_dispatch", "ecommerce.orders_return",
	})

	resp := postDispatch(t, app, order.ID, map[string]any{"carrier_name": "Olva Courier"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Supervisor (orders_dispatch) debe poder despachar, status=%d", resp.StatusCode)
	}
}

func TestCreateDispatchAPI_EcommerceManagePuedeDespachar(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	order := createReadyForDispatchOrder(t, db)
	app := newDispatchTestApp(db, []string{"ecommerce.manage"})

	resp := postDispatch(t, app, order.ID, map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ecommerce.manage debe implicar orders_dispatch, status=%d", resp.StatusCode)
	}
}

// TestCreateDispatchAPI_RespuestaYPersistencia: contrato de la respuesta 201 + confirma que
// Order.Status cambió, vía el endpoint HTTP real (no llamando al servicio directo).
func TestCreateDispatchAPI_RespuestaYPersistencia(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	order := createReadyForDispatchOrder(t, db)
	app := newDispatchTestApp(db, []string{"ecommerce.orders_dispatch"})

	resp := postDispatch(t, app, order.ID, map[string]any{
		"carrier_name": "Olva Courier", "tracking_code": "TRK-1", "package_count": 2, "weight_kg": 3.25,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out struct {
		Data database.TenantEcommerceDispatch `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Data.OrderID != order.ID || out.Data.Status != service.DispatchStatusDespachado {
		t.Fatalf("respuesta inconsistente: %+v", out.Data)
	}

	var updated database.TenantEcommerceOrder
	db.First(&updated, order.ID)
	if updated.Status != service.OrderStatusDespachado {
		t.Fatalf("Order.Status = %q, quería DESPACHADO", updated.Status)
	}
}

func TestCreateDispatchAPI_TransicionInvalida_Rechazada(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	product := database.TenantProduct{Code: "DH2", Name: "Producto", Type: "product", Unit: "NIU", SalePrice: 10, Active: true, ShowInDigitalCatalog: true}
	db.Create(&product)
	svc := service.NewEcommerceService(db)
	order, _, err := svc.CreateOrder(service.CreateOrderInput{
		CustomerName: "X", CustomerPhone: "999", DeliveryMethod: service.DeliveryMethodPickup,
		Items: []service.CreateOrderItemInput{{ProductID: product.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pedido recién creado (PENDIENTE) — nunca debe poder despacharse directo.
	app := newDispatchTestApp(db, []string{"ecommerce.orders_dispatch"})
	resp := postDispatch(t, app, order.ID, map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("despachar un pedido PENDIENTE debe rechazarse (400), status=%d", resp.StatusCode)
	}
}

// ── PATCH /dispatches/:id: solo metadata, nunca 500 por doble click ─────

func TestUpdateDispatchAPI_SoloMetadata(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	order := createReadyForDispatchOrder(t, db)
	app := newDispatchTestApp(db, []string{"ecommerce.orders_dispatch"})

	resp := postDispatch(t, app, order.ID, map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var created struct {
		Data database.TenantEcommerceDispatch `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&created)

	body, _ := json.Marshal(map[string]any{"tracking_code": "ACTUALIZADO"})
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/ecommerce/dispatches/%d", created.Data.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status=%d", resp2.StatusCode)
	}
	var out struct {
		Data database.TenantEcommerceDispatch `json:"data"`
	}
	json.NewDecoder(resp2.Body).Decode(&out)
	if out.Data.TrackingCode == nil || *out.Data.TrackingCode != "ACTUALIZADO" {
		t.Fatalf("tracking_code no se actualizó: %+v", out.Data)
	}
	if out.Data.Status != service.DispatchStatusDespachado {
		t.Fatalf("PATCH no debe poder cambiar Status, quedó %q", out.Data.Status)
	}
}

// TestCreateDispatchAPI_DobleClick_NuncaRetorna500: doble click real vía HTTP — el segundo POST
// debe recibir un rechazo de negocio (400), nunca un 500.
func TestCreateDispatchAPI_DobleClick_NuncaRetorna500(t *testing.T) {
	db := setupDispatchHandlerTestDB(t)
	order := createReadyForDispatchOrder(t, db)
	app := newDispatchTestApp(db, []string{"ecommerce.orders_dispatch"})

	resp1 := postDispatch(t, app, order.ID, map[string]any{})
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("primer POST status=%d", resp1.StatusCode)
	}
	resp2 := postDispatch(t, app, order.ID, map[string]any{})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("segundo POST (doble click) debe ser 400, nunca 500, status=%d", resp2.StatusCode)
	}

	var count int64
	db.Model(&database.TenantEcommerceDispatch{}).Where("order_id = ?", order.ID).Count(&count)
	if count != 1 {
		t.Fatalf("debe existir exactamente 1 despacho tras el doble click, hay %d", count)
	}
}
