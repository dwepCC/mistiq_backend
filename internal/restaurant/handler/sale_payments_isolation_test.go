package handler

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// /sales/:id/payments (GET y POST) debe respetar la sucursal de la venta.
func TestSalePaymentsBranchIsolation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	sales := [2]database.TenantSale{{BranchID: 1}, {BranchID: 2}}
	for i := range sales {
		if err := db.Create(&sales[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	h := New()
	app := newRestaurantIsolationApp(db)
	_ = app
	a := fiber.New()
	a.Use(func(c fiber.Ctx) error {
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
		}
		return c.Next()
	})
	a.Get("/sales/:id/payments", h.GetSalePayments)
	a.Post("/sales/:id/payments", h.RegisterPayments)

	body := map[string]any{"payments": []any{map[string]any{"method": "cash", "amount": 10}}}
	foreign := fmt.Sprintf("/sales/%d/payments", sales[1].ID)
	own := fmt.Sprintf("/sales/%d/payments", sales[0].ID)

	if c, b := restReq(t, a, "s1", "GET", foreign, nil); c != 404 {
		t.Errorf("GET venta ajena: %d (%s), quiere 404", c, b)
	}
	if c, b := restReq(t, a, "s1", "POST", foreign, body); c != 404 {
		t.Errorf("POST venta ajena: %d (%s), quiere 404", c, b)
	}
	// El admin ve la venta de otra sucursal pero no cobra en ella con otra sucursal activa.
	if c, b := restReq(t, a, "admin", "GET", foreign, nil); c != 200 {
		t.Errorf("admin GET venta ajena: %d (%s), quiere 200", c, b)
	}
	if c, b := restReq(t, a, "admin", "POST", foreign, body); c != 404 {
		t.Errorf("admin POST venta ajena: %d (%s), quiere 404", c, b)
	}
	if c, b := restReq(t, a, "s1", "GET", own, nil); c != 200 {
		t.Errorf("GET venta propia: %d (%s), quiere 200", c, b)
	}
}
