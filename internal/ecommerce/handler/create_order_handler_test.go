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

func setupCreateOrderHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantProduct{}, &database.TenantProductPresentation{},
		&database.TenantEcommerceOrder{}, &database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceOrderStatusHistory{}, &database.TenantNotification{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

// newPublicOrderTestApp monta SOLO CreatePublicOrderAPI (sin RequireTenant/RequireEcommerceAvailable
// de routes.go, que ya son infraestructura genérica probada aparte), inyectando tenantDB como hace
// el middleware real.
func newPublicOrderTestApp(db *gorm.DB) *fiber.App {
	h := NewEcommerceHandler()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		return c.Next()
	})
	app.Post("/public/ecommerce/orders", h.CreatePublicOrderAPI)
	return app
}

// TestCreatePublicOrderAPI_CamposDePrecioEnviadosPorElClienteSonIgnorados: puntos 7/8/9 del
// checklist de Fase 3. El body real que llegaría de un cliente manipulado incluye unit_price/
// subtotal/total falsos — el bind de Fiber los deserializa en un struct que NO TIENE esos campos
// (ver CreatePublicOrderAPI), así que ni siquiera llegan al service: es estructuralmente imposible
// que el precio persistido sea el que mandó el cliente.
func TestCreatePublicOrderAPI_CamposDePrecioEnviadosPorElClienteSonIgnorados(t *testing.T) {
	db := setupCreateOrderHandlerTestDB(t)
	product := database.TenantProduct{
		Code: "REAL1", Name: "Producto real", Type: "product", Unit: "NIU", SalePrice: 25,
		Active: true, ShowInDigitalCatalog: true,
	}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}

	app := newPublicOrderTestApp(db)

	rawBody := map[string]interface{}{
		"customer_name":   "Cliente malicioso",
		"customer_phone":  "999111222",
		"delivery_method": "RECOJO_TIENDA",
		"items": []map[string]interface{}{
			{
				"product_id": product.ID,
				"quantity":   2,
				// Campos que un cliente manipulado intentaría inyectar — el struct de bind del
				// handler no los declara, así que Fiber los descarta silenciosamente.
				"unit_price": 0.01,
				"name":       "Precio regalado",
				"subtotal":   0.02,
			},
		},
		"total": 0.02,
	}
	body, _ := json.Marshal(rawBody)
	req := httptest.NewRequest(http.MethodPost, "/public/ecommerce/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var order database.TenantEcommerceOrder
	if err := db.Order("id DESC").First(&order).Error; err != nil {
		t.Fatal(err)
	}
	// 2 unidades x S/ 25 (precio REAL del catálogo) = 50 — nunca 0.02 (lo que mandó el cliente).
	if order.Total != 50 {
		t.Fatalf("REGRESIÓN DE SEGURIDAD: Total = %.2f, el cliente logró manipular el precio (esperado: 50, precio real del catálogo)", order.Total)
	}

	var items []database.TenantEcommerceOrderItem
	db.Where("order_id = ?", order.ID).Find(&items)
	if len(items) != 1 || items[0].UnitPrice != 25 || items[0].Name != "Producto real" {
		t.Fatalf("línea persistida con datos del cliente en vez del catálogo real: %+v", items)
	}
}

// TestCreatePublicOrderAPI_PedidoSimpleHTTP: camino feliz end-to-end vía HTTP (no solo a nivel de
// servicio) — confirma que el contrato JSON real (product_id/presentation_id/quantity, delivery_
// method) funciona de punta a punta.
func TestCreatePublicOrderAPI_PedidoSimpleHTTP(t *testing.T) {
	db := setupCreateOrderHandlerTestDB(t)
	product := database.TenantProduct{
		Code: "HTTP1", Name: "Producto HTTP", Type: "product", Unit: "NIU", SalePrice: 15,
		Active: true, ShowInDigitalCatalog: true,
	}
	db.Create(&product)

	app := newPublicOrderTestApp(db)
	body, _ := json.Marshal(map[string]interface{}{
		"customer_name":   "Cliente real",
		"customer_phone":  "999333444",
		"delivery_method": "RECOJO_TIENDA",
		"items":           []map[string]interface{}{{"product_id": product.ID, "quantity": 3}},
	})
	req := httptest.NewRequest(http.MethodPost, "/public/ecommerce/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var out struct {
		OrderNumber uint `json:"order_number"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.OrderNumber == 0 {
		t.Fatal("order_number debe venir seteado en la respuesta")
	}
}

// TestCreatePublicOrderAPI_ProductoInvalido_Rechaza400: confirma el código de status correcto
// cuando el backend rechaza la validación (no 201, no 500).
func TestCreatePublicOrderAPI_ProductoInvalido_Rechaza400(t *testing.T) {
	db := setupCreateOrderHandlerTestDB(t)
	app := newPublicOrderTestApp(db)
	body, _ := json.Marshal(map[string]interface{}{
		"customer_name": "X", "customer_phone": "999000000", "delivery_method": "RECOJO_TIENDA",
		"items": []map[string]interface{}{{"product_id": 99999, "quantity": 1}},
	})
	req := httptest.NewRequest(http.MethodPost, "/public/ecommerce/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var count int64
	db.Model(&database.TenantEcommerceOrder{}).Count(&count)
	if count != 0 {
		t.Fatalf("un pedido rechazado no debe persistir nada, hay %d filas", count)
	}
}
