package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

func statsApp(db *gorm.DB) *fiber.App {
	h := &DashboardHandler{}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", uint(1))
		switch c.Get("X-U") {
		case "s1":
			c.Locals("active_branch_id", uint(1))
			c.Locals("allowed_branch_ids", []uint{1})
		case "c2":
			c.Locals("active_branch_id", uint(2))
			c.Locals("allowed_branch_ids", []uint{2})
		case "admin":
			c.Locals("active_branch_id", uint(1))
			c.Locals("allowed_branch_ids", []uint{1})
			c.Locals("is_branch_admin", true)
		}
		return c.Next()
	})
	app.Get("/dashboard/stats", h.StatsAPI)
	return app
}

// /dashboard/stats ya no suma todas las sucursales para cualquiera: el usuario ve la suya; quien ve todo, la empresa
// entera salvo que elija una sucursal (?branch_id=).
func TestDashboardStats_BranchScope(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	issue := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local)
	mkSale := func(branch uint, total float64) {
		s := database.TenantSale{BranchID: branch, UserID: 1, SeriesID: 1, DocType: "NOTA DE VENTA", Series: "NV01", Correlative: 1,
			Number: fmt.Sprintf("NV01-%d-%v", branch, total), IssueDate: issue, Total: total, Subtotal: total, Status: "paid"}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
	}
	mkPurchase := func(branch uint, total float64) {
		p := database.TenantPurchase{BranchID: branch, UserID: 1, DocType: "FACTURA", Series: "F001", Number: fmt.Sprint(total), IssueDate: issue, Total: total, Subtotal: total}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
	}
	mkSale(1, 100)
	mkSale(2, 400)
	mkPurchase(1, 10)
	mkPurchase(2, 20)
	app := statsApp(db)

	get := func(user, q string) (sales, purchases float64) {
		req := httptest.NewRequest("GET", "/dashboard/stats"+q, nil)
		req.Header.Set("X-U", user)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("stats %s: %d %s", user, resp.StatusCode, body)
		}
		var out struct {
			Home struct {
				SalesToday     float64 `json:"sales_today"`
				PurchasesToday float64 `json:"purchases_today"`
			} `json:"home"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		return out.Home.SalesToday, out.Home.PurchasesToday
	}
	if s, p := get("s1", ""); s != 100 || p != 10 {
		t.Errorf("S1: ventas=%v compras=%v, quiere 100 y 10 (solo su sucursal)", s, p)
	}
	if s, p := get("s1", "?branch_id=2"); s != 100 || p != 10 {
		t.Errorf("S1 pidiendo branch_id=2: ventas=%v compras=%v, no debe ver la sucursal 2", s, p)
	}
	if s, p := get("c2", ""); s != 400 || p != 20 {
		t.Errorf("S2: ventas=%v compras=%v, quiere 400 y 20", s, p)
	}
	if s, p := get("admin", ""); s != 500 || p != 30 {
		t.Errorf("admin sin sucursal elegida: ventas=%v compras=%v, quiere 500 y 30 (toda la empresa)", s, p)
	}
	if s, p := get("admin", "?branch_id=2"); s != 400 || p != 20 {
		t.Errorf("admin eligiendo S2: ventas=%v compras=%v, quiere 400 y 20", s, p)
	}
}
