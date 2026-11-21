package handler

import (
	"net/http/httptest"
	"testing"

	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func costFromHandler(t *testing.T, perms []string, fn func(c fiber.Ctx) float64) float64 {
	t.Helper()
	app := fiber.New()
	var out float64
	app.Get("/", func(c fiber.Ctx) error {
		c.Locals("tenant_claims", &middleware.TenantClaims{Permissions: perms})
		out = fn(c)
		return nil
	})
	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUpdateCostFromBody_NoPisaElCostoSinPermiso(t *testing.T) {
	upd := func(c fiber.Ctx) float64 { return updateCostFromBody(c, 0, 8.5) }
	if got := costFromHandler(t, []string{"products.edit"}, upd); got != 8.5 {
		t.Fatalf("sin products.view_cost el costo real debe conservarse, quedó %v", got)
	}
	if got := costFromHandler(t, []string{"products.edit", "products.view_cost"}, func(c fiber.Ctx) float64 { return updateCostFromBody(c, 9, 8.5) }); got != 9 {
		t.Fatalf("con permiso se guarda lo enviado, quedó %v", got)
	}
}

func TestCreateCostFromBody_IgnoraCostoSinPermiso(t *testing.T) {
	if got := costFromHandler(t, []string{"products.create"}, func(c fiber.Ctx) float64 { return createCostFromBody(c, 5) }); got != 0 {
		t.Fatalf("sin permiso el costo inicial es 0, quedó %v", got)
	}
}
