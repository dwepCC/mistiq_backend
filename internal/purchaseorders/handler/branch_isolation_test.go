package handler

import (
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"tukifac/config"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Aislamiento entre sucursales de las órdenes de compra: una orden de S1 no se lee, edita, envía ni
// anula desde S2; el administrador que ve todas la lee pero solo opera desde su sucursal activa.

type actor struct {
	user    uint
	active  uint
	allowed []uint
	admin   bool
}

var (
	vendedorS1 = actor{user: 1, active: 1, allowed: []uint{1}}
	cajeroS2   = actor{user: 2, active: 2, allowed: []uint{2}}
	adminS1    = actor{user: 9, active: 1, allowed: []uint{1, 2}, admin: true}
	adminS2    = actor{user: 9, active: 2, allowed: []uint{1, 2}, admin: true}
)

func newApp(db *gorm.DB) *fiber.App {
	h := NewPurchaseOrderHandler()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		u, _ := strconv.Atoi(c.Get("X-User"))
		c.Locals("user_id", uint(u))
		a, _ := strconv.Atoi(c.Get("X-Active"))
		c.Locals("active_branch_id", uint(a))
		var allowed []uint
		for _, p := range strings.Split(c.Get("X-Allowed"), ",") {
			if n, err := strconv.Atoi(p); err == nil {
				allowed = append(allowed, uint(n))
			}
		}
		c.Locals("allowed_branch_ids", allowed)
		c.Locals("is_branch_admin", c.Get("X-Admin") == "1")
		return c.Next()
	})
	app.Get("/po", h.ListAPI)
	app.Get("/po/:id", h.GetAPI)
	app.Post("/po", h.CreateAPI)
	app.Patch("/po/:id", h.UpdateAPI)
	app.Post("/po/:id/send", h.SendAPI)
	app.Post("/po/:id/cancel", h.CancelAPI)
	return app
}

func do(t *testing.T, app *fiber.App, a actor, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User", strconv.Itoa(int(a.user)))
	req.Header.Set("X-Active", strconv.Itoa(int(a.active)))
	var ids []string
	for _, id := range a.allowed {
		ids = append(ids, strconv.Itoa(int(id)))
	}
	req.Header.Set("X-Allowed", strings.Join(ids, ","))
	if a.admin {
		req.Header.Set("X-Admin", "1")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, e := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if e != nil {
			break
		}
	}
	return resp.StatusCode, sb.String()
}

func setup(t *testing.T) (*fiber.App, uint, uint) {
	t.Helper()
	prev := config.AppConfig
	config.AppConfig = &config.Config{}
	t.Cleanup(func() { config.AppConfig = prev })
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantBranch{}, &database.TenantDocumentSeries{}, &database.TenantContact{}, &database.TenantProduct{},
		&database.TenantPurchaseOrder{}, &database.TenantPurchaseOrderItem{}, &database.TenantCompanyConfig{},
	); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantBranch{Name: "Principal", IsMain: true, Active: true})
	db.Create(&database.TenantBranch{Name: "Sucursal 2", Active: true})
	s1 := database.TenantDocumentSeries{BranchID: 1, DocType: "Orden de compra", SunatCode: "OC", Category: "orden_compra", Series: "OC01", Correlative: 1, Active: true}
	s2 := database.TenantDocumentSeries{BranchID: 2, DocType: "Orden de compra", SunatCode: "OC", Category: "orden_compra", Series: "OC02", Correlative: 1, Active: true}
	db.Create(&s1)
	db.Create(&s2)
	db.Create(&database.TenantContact{Type: "supplier", DocType: "RUC", DocNumber: "20123456789", BusinessName: "Proveedor SAC"})
	return newApp(db), s1.ID, s2.ID
}

func body(seriesID uint, desc string) string {
	return fmt.Sprintf(`{"contact_id":1,"series_id":%d,"currency":"PEN","items":[{"description":%q,"quantity":2,"unit_cost":10,"igv_affectation_type":"10"}]}`, seriesID, desc)
}

func TestPurchaseOrders_BranchIsolation(t *testing.T) {
	app, s1, s2 := setup(t)

	if code, out := do(t, app, vendedorS1, "POST", "/po", body(s1, "A")); code != 201 {
		t.Fatalf("crear en S1: %d %s", code, out)
	}
	// Una serie de otra sucursal no sirve aunque el usuario la conozca.
	if code, _ := do(t, app, vendedorS1, "POST", "/po", body(s2, "A")); code != 400 {
		t.Errorf("serie de S2 desde S1 debe rechazarse, got %d", code)
	}
	if code, out := do(t, app, cajeroS2, "POST", "/po", body(s2, "B")); code != 201 {
		t.Fatalf("crear en S2: %d %s", code, out)
	}

	// Listado: cada sucursal ve solo la suya.
	_, l1 := do(t, app, vendedorS1, "GET", "/po", "")
	if !strings.Contains(l1, "OC01-") || strings.Contains(l1, "OC02-") {
		t.Errorf("S1 debe ver solo OC01: %s", l1)
	}
	_, l2 := do(t, app, cajeroS2, "GET", "/po", "")
	if !strings.Contains(l2, "OC02-") || strings.Contains(l2, "OC01-") {
		t.Errorf("S2 debe ver solo OC02: %s", l2)
	}

	// La orden 1 es de S1: S2 recibe 404 en lectura y en toda operación (sin revelar que existe).
	for _, c := range []struct{ method, path, b string }{
		{"GET", "/po/1", ""},
		{"PATCH", "/po/1", body(s2, "hack")},
		{"POST", "/po/1/send", ""},
		{"POST", "/po/1/cancel", ""},
	} {
		if code, _ := do(t, app, cajeroS2, c.method, c.path, c.b); code != 404 {
			t.Errorf("%s %s desde S2: %d, quiere 404", c.method, c.path, code)
		}
	}
	// Sigue intacta.
	if _, out := do(t, app, vendedorS1, "GET", "/po/1", ""); !strings.Contains(out, `"status":"draft"`) {
		t.Errorf("la orden de S1 no debe haber cambiado: %s", out)
	}

	// Administrador con sucursal activa S2: LEE la orden de S1, pero no la opera (403).
	if code, _ := do(t, app, adminS2, "GET", "/po/1", ""); code != 200 {
		t.Errorf("admin lee orden de otra sucursal: %d", code)
	}
	if code, _ := do(t, app, adminS2, "POST", "/po/1/cancel", ""); code != 403 {
		t.Errorf("admin no opera desde otra sucursal activa: %d, quiere 403", code)
	}
	// Desde su sucursal activa sí.
	if code, _ := do(t, app, adminS1, "POST", "/po/1/send", ""); code != 200 {
		t.Errorf("admin opera desde la sucursal activa: %d", code)
	}
}
