package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

func convertReq(t *testing.T, app *fiber.App, user string, branchID uint) (int, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"target": "sale", "series_id": 1, "branch_id": branchID})
	req := httptest.NewRequest("POST", "/ecommerce/orders/1/convert", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-U", user)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// El branch_id del body de ConvertOrderAPI ya no es de confianza: solo la sucursal activa o una accesible.
func TestConvertOrder_BranchIDFromBodyIsValidated(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	h := NewEcommerceHandler()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", uint(1))
		switch c.Get("X-U") {
		case "s1":
			c.Locals("active_branch_id", uint(1))
			c.Locals("allowed_branch_ids", []uint{1})
		case "admin":
			c.Locals("active_branch_id", uint(1))
			c.Locals("allowed_branch_ids", []uint{1})
			c.Locals("is_branch_admin", true)
		case "adminall":
			c.Locals("active_branch_id", uint(0))
			c.Locals("is_branch_admin", true)
		}
		return c.Next()
	})
	app.Post("/ecommerce/orders/:id/convert", h.ConvertOrderAPI)

	if c, b := convertReq(t, app, "s1", 2); c != 403 {
		t.Errorf("vendedor S1 con branch_id=2: %d (%s), quiere 403", c, b)
	}
	if c, b := convertReq(t, app, "adminall", 1); c != 403 {
		t.Errorf("admin sin sucursal activa (modo todas): %d (%s), quiere 403", c, b)
	}
	if c, b := convertReq(t, app, "s1", 0); c != 400 {
		t.Errorf("branch_id ausente conserva su contrato (400): %d (%s)", c, b)
	}
	// Caso propio: la guarda deja pasar (el pedido no existe -> 400 del servicio, no 403).
	if c, b := convertReq(t, app, "s1", 1); c == 403 {
		t.Errorf("vendedor S1 con su sucursal: %d (%s), la guarda no debe bloquearlo", c, b)
	}
	// Ni siquiera quien ve todo escribe en otra sucursal por el body: debe cambiar de sucursal activa.
	if c, b := convertReq(t, app, "admin", 2); c != 403 {
		t.Errorf("admin con branch_id=2 y activa 1: %d (%s), quiere 403", c, b)
	}
}
