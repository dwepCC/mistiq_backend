package middleware

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tukifac/config"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

func sseAuthApp() *fiber.App {
	app := fiber.New()
	app.Use(TenantAuthAPI())
	ok := func(c fiber.Ctx) error { return c.SendString("pasó") }
	app.Get("/api/billing/events", ok)
	app.Get("/api/sales", ok)
	app.Post("/api/billing/events", ok)
	return app
}

func doReq(t *testing.T, app *fiber.App, method, url string) (int, string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(method, url, nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Con un token basura se distingue "el middleware LEYÓ el query" ("Token inválido o expirado") de "lo ignoró"
// ("Token no proporcionado").
func TestSSEQueryToken_OnlyOnEventsRoute_BridgeOn(t *testing.T) {
	t.Setenv("SSE_ALLOW_QUERY_TOKEN", "")
	app := sseAuthApp()

	before := SSEQueryTokenConnections()
	if code, body := doReq(t, app, "GET", "/api/billing/events?access_token=basura"); code != 401 || !strings.Contains(body, "inválido") {
		t.Fatalf("en /api/billing/events el query debe leerse: %d %s", code, body)
	}
	if SSEQueryTokenConnections() != before+1 {
		t.Fatal("el uso del puente debe contarse")
	}
	if code, body := doReq(t, app, "GET", "/api/billing/events/?access_token=basura"); code != 401 || !strings.Contains(body, "inválido") {
		t.Fatalf("con barra final también: %d %s", code, body)
	}
	for _, u := range []string{"/api/sales?access_token=basura", "/api/billing/events2?access_token=basura"} {
		if code, body := doReq(t, app, "GET", u); code != 401 || !strings.Contains(body, "no proporcionado") {
			t.Fatalf("%s: el query debe ignorarse: %d %s", u, code, body)
		}
	}
	if code, body := doReq(t, app, "POST", "/api/billing/events?access_token=basura"); code != 401 || !strings.Contains(body, "no proporcionado") {
		t.Fatalf("solo GET puede usar el puente: %d %s", code, body)
	}
}

func TestSSEQueryToken_BridgeOff(t *testing.T) {
	t.Setenv("SSE_ALLOW_QUERY_TOKEN", "0")
	app := sseAuthApp()
	before := SSEQueryTokenConnections()
	if code, body := doReq(t, app, "GET", "/api/billing/events?access_token=basura"); code != 401 || !strings.Contains(body, "no proporcionado") {
		t.Fatalf("con SSE_ALLOW_QUERY_TOKEN=0 el query debe ignorarse: %d %s", code, body)
	}
	if SSEQueryTokenConnections() != before {
		t.Fatal("no debe contar si el puente está apagado")
	}
	// La cabecera sigue siendo la vía normal: se lee (token basura => inválido, no "no proporcionado").
	req := httptest.NewRequest("GET", "/api/billing/events", nil)
	req.Header.Set("Authorization", "Bearer basura")
	resp, _ := app.Test(req)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 401 || !strings.Contains(string(b), "inválido") {
		t.Fatalf("la cabecera debe seguir funcionando: %d %s", resp.StatusCode, b)
	}
}

func TestSSEQueryToken_RateLimitTenantID(t *testing.T) {
	config.AppConfig = &config.Config{JWTSecret: "secreto-de-prueba-sse"}
	claims := &TenantClaims{TenantID: 77, Type: "tenant", RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(config.AppConfig.JWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/*", func(c fiber.Ctx) error { return c.SendString(strings.TrimSpace(string(rune('0' + verifiedTenantID(c)%10)))) })

	t.Setenv("SSE_ALLOW_QUERY_TOKEN", "")
	if _, b := doReq(t, app, "GET", "/api/billing/events?access_token="+tok); b != "7" {
		t.Fatalf("ratelimit debe leer el tenant del query en la ruta SSE, got %q", b)
	}
	if _, b := doReq(t, app, "GET", "/api/sales?access_token="+tok); b != "0" {
		t.Fatalf("ratelimit no debe leer el query en otras rutas, got %q", b)
	}
	t.Setenv("SSE_ALLOW_QUERY_TOKEN", "0")
	if _, b := doReq(t, app, "GET", "/api/billing/events?access_token="+tok); b != "0" {
		t.Fatalf("con puente apagado tampoco, got %q", b)
	}
}
