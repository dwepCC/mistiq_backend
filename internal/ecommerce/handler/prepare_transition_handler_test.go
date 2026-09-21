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

var prepareHandlerTestDBCounter int

func setupPrepareHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	prepareHandlerTestDBCounter++
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), prepareHandlerTestDBCounter)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
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

// newPrepareTestApp app real con el endpoint PUT /status y tenant_claims/user_id inyectados en
// Locals — MISMO mecanismo que usa TenantAuthAPI en producción (pkg/middleware/auth.go), así que
// hasEcommercePermission (ecommerce_scope.go) se ejercita de verdad, no se mockea.
func newPrepareTestApp(db *gorm.DB, permissions []string) *fiber.App {
	h := NewEcommerceHandler()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", uint(7))
		c.Locals("tenant_claims", &middleware.TenantClaims{UserID: 7, Permissions: permissions})
		return c.Next()
	})
	app.Put("/api/ecommerce/orders/:id/status", h.UpdateOrderStatusAPI)
	return app
}

func createConfirmedOrderForPrepareTests(t *testing.T, db *gorm.DB) *database.TenantEcommerceOrder {
	t.Helper()
	product := database.TenantProduct{Code: "PH1", Name: "Producto handler", Type: "product", Unit: "NIU", SalePrice: 20, Active: true, ShowInDigitalCatalog: true}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	svc := service.NewEcommerceService(db)
	order, _, err := svc.CreateOrder(service.CreateOrderInput{
		CustomerName: "Cliente", CustomerPhone: "999000111", DeliveryMethod: service.DeliveryMethodPickup,
		Items: []service.CreateOrderItemInput{{ProductID: product.ID, Quantity: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateOrderStatus(order.ID, service.UpdateOrderStatusInput{NewStatus: service.OrderStatusConfirmado}); err != nil {
		t.Fatal(err)
	}
	return order
}

func putOrderStatus(t *testing.T, app *fiber.App, orderID uint, status string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"status": status})
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/ecommerce/orders/%d/status", orderID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestUpdateOrderStatusAPI_AlmaceneroPuedePrepararPeroNoGestionar Fase 7 (checklist puntos 1/2):
// un usuario con SOLO ecommerce.orders_prepare (Almacenero real) puede ejecutar la transición de
// preparación, pero el mismo endpoint rechaza una transición de gestión (confirmar/cancelar) que
// exige ecommerce.orders_manage — un permiso nunca implica el otro.
func TestUpdateOrderStatusAPI_AlmaceneroPuedePrepararPeroNoGestionar(t *testing.T) {
	db := setupPrepareHandlerTestDB(t)
	order := createConfirmedOrderForPrepareTests(t, db)
	app := newPrepareTestApp(db, []string{"ecommerce.orders_view", "ecommerce.orders_prepare"})

	resp := putOrderStatus(t, app, order.ID, "EN_PREPARACION")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Almacenero (orders_prepare) debía poder iniciar preparación, status=%d", resp.StatusCode)
	}

	resp2 := putOrderStatus(t, app, order.ID, "CANCELADO")
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("Almacenero (sin orders_manage) NO debía poder cancelar, status=%d", resp2.StatusCode)
	}
}

// TestUpdateOrderStatusAPI_UsuarioSinOrdersPrepare_Rechazado: el inverso — orders_manage por sí
// solo NO debe alcanzar para preparar (Vendedor no recibe automáticamente permisos de preparación
// solo por poder gestionar pedidos, regla explícita de Fase 7).
func TestUpdateOrderStatusAPI_UsuarioSinOrdersPrepare_Rechazado(t *testing.T) {
	db := setupPrepareHandlerTestDB(t)
	order := createConfirmedOrderForPrepareTests(t, db)
	app := newPrepareTestApp(db, []string{"ecommerce.orders_view", "ecommerce.orders_manage", "ecommerce.orders_convert"})

	resp := putOrderStatus(t, app, order.ID, "EN_PREPARACION")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("un Vendedor (orders_manage+orders_convert, SIN orders_prepare) no debe poder iniciar preparación, status=%d", resp.StatusCode)
	}
}

// TestUpdateOrderStatusAPI_ManageImplicaPreparar: ecommerce.manage sigue implicando todo, mismo
// criterio ya usado en el resto del RBAC de este módulo desde Fase 1.
func TestUpdateOrderStatusAPI_ManageImplicaPreparar(t *testing.T) {
	db := setupPrepareHandlerTestDB(t)
	order := createConfirmedOrderForPrepareTests(t, db)
	app := newPrepareTestApp(db, []string{"ecommerce.manage"})

	resp := putOrderStatus(t, app, order.ID, "EN_PREPARACION")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ecommerce.manage debe implicar orders_prepare, status=%d", resp.StatusCode)
	}
}

// TestUpdateOrderStatusAPI_TransicionInvalida_422: saltar una etapa (CONFIRMADO->EMPAQUETADO,
// sin pasar por EN_PREPARACION) debe rechazarse aunque el usuario tenga TODOS los permisos.
func TestUpdateOrderStatusAPI_TransicionInvalida_422(t *testing.T) {
	db := setupPrepareHandlerTestDB(t)
	order := createConfirmedOrderForPrepareTests(t, db)
	app := newPrepareTestApp(db, []string{"ecommerce.manage"})

	resp := putOrderStatus(t, app, order.ID, "EMPAQUETADO")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("saltar de CONFIRMADO a EMPAQUETADO debe rechazarse (422), status=%d", resp.StatusCode)
	}
}

// TestUpdateOrderStatusAPI_PreparacionRegistraStatusHistory: la transición vía HTTP deja rastro
// real, nunca fabricado.
func TestUpdateOrderStatusAPI_PreparacionRegistraStatusHistory(t *testing.T) {
	db := setupPrepareHandlerTestDB(t)
	order := createConfirmedOrderForPrepareTests(t, db)
	app := newPrepareTestApp(db, []string{"ecommerce.orders_prepare"})

	resp := putOrderStatus(t, app, order.ID, "EN_PREPARACION")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	var hist database.TenantEcommerceOrderStatusHistory
	if err := db.Where("order_id = ? AND to_status = ?", order.ID, "EN_PREPARACION").First(&hist).Error; err != nil {
		t.Fatalf("debía registrarse una fila de StatusHistory para la transición: %v", err)
	}
	if hist.UserID == nil || *hist.UserID != 7 {
		t.Errorf("StatusHistory.UserID debía ser 7 (derivado del token, ver user_id en Locals), quedó %v", hist.UserID)
	}
}
