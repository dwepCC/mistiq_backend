package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

func setupCustomerHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantEcommerceCustomerAccount{}, &database.TenantEcommerceCustomerAddress{},
		&database.TenantProduct{}, &database.TenantProductPresentation{},
		&database.TenantEcommerceOrder{}, &database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceOrderStatusHistory{}, &database.TenantEcommerceDispatch{},
		&database.TenantEcommerceDispatchStatusHistory{}, &database.TenantNotification{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

// withEcommerceCustomerJWTSecret inicializa config.AppConfig con un secreto de prueba — mismo
// patrón que internal/superadmin/fase10_rbac_qa_test.go (config.AppConfig = &config.Config{...} +
// t.Cleanup para restaurar).
func withEcommerceCustomerJWTSecret(t *testing.T) {
	t.Helper()
	prev := config.AppConfig
	config.AppConfig = &config.Config{
		AppEnv:                     "development",
		JWTSecret:                  "staff-secret-test",
		EcommerceCustomerJWTSecret: "customer-secret-test",
	}
	t.Cleanup(func() { config.AppConfig = prev })
}

func newCustomerTestApp(db *gorm.DB) *fiber.App {
	h := NewEcommerceHandler()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		return c.Next()
	})
	app.Post("/public/ecommerce/auth/register", h.RegisterCustomerAPI)
	app.Post("/public/ecommerce/auth/login", h.LoginCustomerAPI)
	app.Post("/public/ecommerce/orders", middleware.EcommerceCustomerAuthOptional(), h.CreatePublicOrderAPI)
	acc := app.Group("/public/ecommerce/account", middleware.EcommerceCustomerAuthRequired())
	acc.Get("/me", h.CustomerMeAPI)
	acc.Get("/orders", h.ListCustomerOrdersAPI)
	acc.Get("/orders/:id", h.GetCustomerOrderAPI)
	acc.Post("/addresses", h.CreateCustomerAddressAPI)
	return app
}

func doJSON(t *testing.T, app *fiber.App, method, path string, body interface{}, bearer string) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// ── 7/10. Token válido concede acceso / ausencia de token rechaza ──

func TestCustomerMeAPI_TokenValido_Concede(t *testing.T) {
	withEcommerceCustomerJWTSecret(t)
	db := setupCustomerHandlerTestDB(t)
	app := newCustomerTestApp(db)

	regResp := doJSON(t, app, http.MethodPost, "/public/ecommerce/auth/register",
		map[string]string{"name": "Cliente", "phone": "999111000", "password": "clave123"}, "")
	var reg struct {
		Token string `json:"token"`
	}
	json.NewDecoder(regResp.Body).Decode(&reg)
	if reg.Token == "" {
		t.Fatalf("registro debe devolver un token, status=%d", regResp.StatusCode)
	}

	meResp := doJSON(t, app, http.MethodGet, "/public/ecommerce/account/me", nil, reg.Token)
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("con token válido, /me debe responder 200, dio %d", meResp.StatusCode)
	}
}

func TestCustomerMeAPI_SinToken_Rechazado(t *testing.T) {
	withEcommerceCustomerJWTSecret(t)
	db := setupCustomerHandlerTestDB(t)
	app := newCustomerTestApp(db)

	resp := doJSON(t, app, http.MethodGet, "/public/ecommerce/account/me", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("sin token, /me debe responder 401, dio %d", resp.StatusCode)
	}
}

// ── 8. Token inválido (mal firmado / expirado / de otro tipo) ──────

func TestCustomerMeAPI_TokenInvalido_Rechazado(t *testing.T) {
	withEcommerceCustomerJWTSecret(t)
	db := setupCustomerHandlerTestDB(t)
	app := newCustomerTestApp(db)

	cases := map[string]string{
		"basura":           "esto-no-es-un-jwt",
		"firmado_distinto": mustSignToken(t, "otro-secreto-cualquiera", "ecommerce_customer", time.Hour),
		"expirado":         mustSignToken(t, "customer-secret-test", "ecommerce_customer", -time.Hour),
		"tipo_incorrecto":  mustSignToken(t, "customer-secret-test", "tenant", time.Hour),
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			resp := doJSON(t, app, http.MethodGet, "/public/ecommerce/account/me", nil, token)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s: esperaba 401, dio %d", name, resp.StatusCode)
			}
		})
	}
}

func mustSignToken(t *testing.T, secret, tokenType string, ttl time.Duration) string {
	t.Helper()
	claims := jwt.MapClaims{
		"customer_account_id": 1,
		"type":                tokenType,
		"exp":                 time.Now().Add(ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// ── 9. Token de cliente rechazado por endpoint de STAFF (y viceversa) ──
// Prueba de aislamiento real: no es solo "el campo type no coincide", es que la FIRMA ni siquiera
// valida con el secreto equivocado — un token de cliente jamás puede colarse como TenantClaims.

func TestEcommerceCustomerToken_RechazadoPorEndpointDeStaff(t *testing.T) {
	withEcommerceCustomerJWTSecret(t)
	customerToken, err := middleware.BuildEcommerceCustomerToken(1, "999000000", "demo")
	if err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	app.Get("/staff-only", middleware.TenantAuthAPI(), func(c fiber.Ctx) error { return c.SendStatus(200) })

	req := httptest.NewRequest(http.MethodGet, "/staff-only", nil)
	req.Header.Set("Authorization", "Bearer "+customerToken)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("un token de cliente NUNCA debe ser aceptado por un endpoint de staff, dio status %d", resp.StatusCode)
	}
}

func TestTenantStaffToken_RechazadoPorEndpointDeCliente(t *testing.T) {
	withEcommerceCustomerJWTSecret(t)
	db := setupCustomerHandlerTestDB(t)
	app := newCustomerTestApp(db)

	// Token de STAFF firmado con JWTSecret (secreto distinto) — nunca debe validar contra
	// EcommerceCustomerAuthRequired (secreto EcommerceCustomerJWTSecret).
	staffToken := mustSignToken(t, "staff-secret-test", "tenant", time.Hour)
	resp := doJSON(t, app, http.MethodGet, "/public/ecommerce/account/me", nil, staffToken)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("un token de staff NUNCA debe ser aceptado por un endpoint de cliente, dio status %d", resp.StatusCode)
	}
}

// ── 19/20. Checkout autenticado: CustomerAccountID SIEMPRE del token ──

func TestCreatePublicOrderAPI_AutenticadoUsaCustomerAccountIDDelToken(t *testing.T) {
	withEcommerceCustomerJWTSecret(t)
	db := setupCustomerHandlerTestDB(t)
	product := database.TenantProduct{Code: "AUTH1", Name: "Producto", Type: "product", Unit: "NIU", SalePrice: 10, Active: true, ShowInDigitalCatalog: true}
	db.Create(&product)
	app := newCustomerTestApp(db)

	regResp := doJSON(t, app, http.MethodPost, "/public/ecommerce/auth/register",
		map[string]string{"name": "Cliente", "phone": "999444555", "password": "clave123"}, "")
	var reg struct {
		Token    string `json:"token"`
		Customer struct{ ID uint }
	}
	json.NewDecoder(regResp.Body).Decode(&reg)

	// El body NO tiene ningún campo customer_account_id — CreatePublicOrderAPI lo toma del token.
	orderResp := doJSON(t, app, http.MethodPost, "/public/ecommerce/orders", map[string]interface{}{
		"customer_name": "Cliente", "customer_phone": "999444555", "delivery_method": "RECOJO_TIENDA",
		"items": []map[string]interface{}{{"product_id": product.ID, "quantity": 1}},
	}, reg.Token)
	if orderResp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", orderResp.StatusCode)
	}

	var order database.TenantEcommerceOrder
	db.Order("id DESC").First(&order)
	if order.CustomerAccountID == nil {
		t.Fatal("el pedido debe quedar vinculado a la cuenta autenticada, quedó como invitado")
	}
	// Confirma en /account/orders que el pedido aparece para ESTE cliente.
	listResp := doJSON(t, app, http.MethodGet, "/public/ecommerce/account/orders", nil, reg.Token)
	var list struct {
		Data []database.TenantEcommerceOrder `json:"data"`
	}
	json.NewDecoder(listResp.Body).Decode(&list)
	if len(list.Data) != 1 {
		t.Fatalf("el cliente debe ver su pedido recién creado en /account/orders, vio %d", len(list.Data))
	}
}

// ── 23. Pedido de invitado (sin token) sigue funcionando ────────────

func TestCreatePublicOrderAPI_InvitadoSigueFuncionando(t *testing.T) {
	withEcommerceCustomerJWTSecret(t)
	db := setupCustomerHandlerTestDB(t)
	product := database.TenantProduct{Code: "GUEST1", Name: "Producto invitado", Type: "product", Unit: "NIU", SalePrice: 12, Active: true, ShowInDigitalCatalog: true}
	db.Create(&product)
	app := newCustomerTestApp(db)

	resp := doJSON(t, app, http.MethodPost, "/public/ecommerce/orders", map[string]interface{}{
		"customer_name": "Invitado", "customer_phone": "999777888", "delivery_method": "RECOJO_TIENDA",
		"items": []map[string]interface{}{{"product_id": product.ID, "quantity": 1}},
	}, "") // sin Authorization — EcommerceCustomerAuthOptional no debe bloquear esto
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("un pedido de invitado (sin token) debe seguir funcionando, status = %d", resp.StatusCode)
	}
	var order database.TenantEcommerceOrder
	db.Order("id DESC").First(&order)
	if order.CustomerAccountID != nil {
		t.Fatal("un pedido de invitado no debe quedar vinculado a ninguna cuenta")
	}
}
