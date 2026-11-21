package middleware

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"tukifac/pkg/logger"

	"github.com/gofiber/fiber/v3"
)

func TestRequestLogger_ClientIPOnlyOutsideRUM(t *testing.T) {
	buf := &bytes.Buffer{}
	prev := logger.L
	logger.L = slog.New(slog.NewJSONHandler(buf, nil))
	defer func() { logger.L = prev }()

	app := fiber.New()
	app.Use(RequestLogger())
	app.Get("/api/algo", func(c fiber.Ctx) error { return c.SendString("ok") })
	app.Post("/api/public/rum", func(c fiber.Ctx) error { return c.SendStatus(204) })

	if _, err := app.Test(httptest.NewRequest("GET", "/api/algo", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Test(httptest.NewRequest("POST", "/api/public/rum", strings.NewReader("{}"))); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("esperaba 2 líneas de log, hubo %d: %s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], `"client_ip"`) {
		t.Errorf("las peticiones normales deben registrar client_ip: %s", lines[0])
	}
	if strings.Contains(lines[1], `"client_ip"`) {
		t.Errorf("la telemetría RUM NO debe registrar client_ip: %s", lines[1])
	}
}
