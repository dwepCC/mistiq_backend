package routes

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
)

// El preflight de una lectura en modo "Todas las sucursales" debe aceptar X-Branch-Mode; si no, fuera del proxy de desarrollo
// (producción, Tauri, Android) toda lectura de ese modo falla en el navegador.
func TestCORSPreflightAllowsBranchModeHeader(t *testing.T) {
	app := fiber.New()
	app.Use(cors.New(cors.Config{
		AllowOriginsFunc: func(string) bool { return true },
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     CORSAllowHeaders,
		AllowCredentials: true,
	}))
	app.Get("/api/sales", func(c fiber.Ctx) error { return c.SendString("ok") })

	req := httptest.NewRequest("OPTIONS", "/api/sales", nil)
	req.Header.Set("Origin", "https://app.tukifac.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "authorization,x-branch-mode,x-tenant-slug")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	allow := strings.ToLower(resp.Header.Get("Access-Control-Allow-Headers"))
	if !strings.Contains(allow, "x-branch-mode") {
		t.Errorf("Access-Control-Allow-Headers=%q no incluye X-Branch-Mode", allow)
	}
}
