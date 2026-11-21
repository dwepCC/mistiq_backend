package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantmigrations"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Escenario (Fase 3A): 2 sucursales + una ajena. Usuarios por cabecera X-U:
//
//	s1: vendedor S1   c2: cajero S2   admin: Administrador (ve todo) con S1 activa   adminall: Administrador sin sucursal activa
func newProductIsolationApp(db *gorm.DB) *fiber.App {
	h := &ProductHandler{}
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
		case "adminall":
			c.Locals("active_branch_id", uint(0))
			c.Locals("is_branch_admin", true)
		}
		return c.Next()
	})
	app.Get("/products", h.SearchAPI)
	app.Post("/products", h.CreateAPI)
	app.Get("/products/:id", h.GetAPI)
	app.Put("/products/:id", h.UpdateAPI)
	app.Patch("/products/bulk-branches", h.BulkBranchesAPI)
	app.Patch("/products/bulk-update-prices", h.BulkUpdatePricesAPI)
	app.Get("/products/:id/branch-prices", h.ProductBranchPriceListAPI)
	app.Get("/products/:id/branch-prices/:branchId", h.ProductBranchPriceGetAPI)
	app.Post("/products/:id/branch-prices/:branchId", h.ProductBranchPriceCreateAPI)
	app.Put("/products/:id/branch-prices/:branchId", h.ProductBranchPriceUpdateAPI)
	app.Delete("/products/:id/branch-prices/:branchId", h.ProductBranchPriceDeleteAPI)
	return app
}

func prodReq(t *testing.T, app *fiber.App, user, method, path string, body any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("X-U", user)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

type prodIso struct {
	app      *fiber.App
	db       *gorm.DB
	all      database.TenantProduct // sin asignación: visible en todas
	onlyS2   database.TenantProduct // asignado solo a S2 (puente)
	both     database.TenantProduct // asignado a S1 y S2
	legacyS2 database.TenantProduct // dueña legacy branch_id = 2
}

func setupProductIsolation(t *testing.T) prodIso {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := (tenantmigrations.V177ProductBranches{}).Up(db); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantProductBranchPrice{}); err != nil {
		t.Fatal(err)
	}
	for i, n := range []string{"Principal", "Sucursal 2", "Sucursal 3"} {
		if err := db.Create(&database.TenantBranch{Name: n, Active: true, IsMain: i == 0}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk := func(code string, legacyBranch uint) database.TenantProduct {
		p := database.TenantProduct{Code: code, Name: "Prod " + code, Type: "product", Unit: "NIU", SalePrice: 10, Active: true, BranchID: legacyBranch}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		return p
	}
	f := prodIso{db: db, app: newProductIsolationApp(db)}
	f.all = mk("ALL", 0)
	f.onlyS2 = mk("S2", 0)
	f.both = mk("BOTH", 0)
	f.legacyS2 = mk("LEG", 2)
	for _, r := range []database.TenantProductBranch{
		{ProductID: f.onlyS2.ID, BranchID: 2},
		{ProductID: f.both.ID, BranchID: 1}, {ProductID: f.both.ID, BranchID: 2},
	} {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestProducts_ByIDUsesBranchBridge(t *testing.T) {
	f := setupProductIsolation(t)
	get := func(u string, id uint) int {
		c, _ := prodReq(t, f.app, u, "GET", fmt.Sprintf("/products/%d", id), nil)
		return c
	}
	if c := get("s1", f.all.ID); c != 200 {
		t.Errorf("S1 leyendo producto sin asignación: %d, quiere 200", c)
	}
	if c := get("s1", f.both.ID); c != 200 {
		t.Errorf("S1 leyendo producto asignado a S1 y S2: %d, quiere 200", c)
	}
	if c := get("s1", f.onlyS2.ID); c != 404 {
		t.Errorf("S1 leyendo producto asignado solo a S2: %d, quiere 404", c)
	}
	if c := get("c2", f.onlyS2.ID); c != 200 {
		t.Errorf("S2 leyendo su producto: %d, quiere 200", c)
	}
	if c := get("admin", f.onlyS2.ID); c != 200 {
		t.Errorf("admin (ve todo) leyendo producto de S2: %d, quiere 200", c)
	}
	// Dueña legacy (branch_id): contrato previo, 403.
	if c := get("s1", f.legacyS2.ID); c != 403 {
		t.Errorf("S1 leyendo producto con branch_id legacy 2: %d, quiere 403", c)
	}
	// Editar un producto invisible también es 404.
	if c, _ := prodReq(t, f.app, "s1", "PUT", fmt.Sprintf("/products/%d", f.onlyS2.ID), map[string]any{"name": "x"}); c != 404 {
		t.Errorf("S1 editando producto asignado solo a S2: %d, quiere 404", c)
	}
}

func TestProducts_ListDefaultsToVisibleForActiveBranch(t *testing.T) {
	f := setupProductIsolation(t)
	ids := func(u, q string) map[uint]bool {
		code, body := prodReq(t, f.app, u, "GET", "/products"+q, nil)
		if code != 200 {
			t.Fatalf("listado %s: %d %s", u, code, body)
		}
		var out struct {
			Data []struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		m := map[uint]bool{}
		for _, r := range out.Data {
			m[r.ID] = true
		}
		return m
	}
	s1 := ids("s1", "")
	if !s1[f.all.ID] || !s1[f.both.ID] {
		t.Errorf("S1 debe ver el producto sin asignación y el asignado a S1: %v", s1)
	}
	if s1[f.onlyS2.ID] {
		t.Errorf("S1 NO debe ver el producto asignado solo a S2: %v", s1)
	}
	// branch_id ajeno pedido por S1: no se acepta, cae a la activa.
	if m := ids("s1", "?branch_id=2"); m[f.onlyS2.ID] {
		t.Errorf("S1 pidiendo branch_id=2 no debe ver productos solo de S2: %v", m)
	}
	c2 := ids("c2", "")
	if !c2[f.onlyS2.ID] || !c2[f.both.ID] {
		t.Errorf("S2 debe ver sus productos asignados: %v", c2)
	}
	// Quien ve todo conserva la vista completa del catálogo compartido (sin branch_id) y puede acotar a una sucursal.
	adm := ids("admin", "")
	if !adm[f.onlyS2.ID] || !adm[f.all.ID] {
		t.Errorf("admin sin branch_id debe ver el catálogo completo: %v", adm)
	}
	if m := ids("admin", "?branch_id=2"); !m[f.onlyS2.ID] {
		t.Errorf("admin con branch_id=2 debe ver los productos de S2: %v", m)
	}
}

func TestProducts_BranchAssignmentLimitedToAccessible(t *testing.T) {
	f := setupProductIsolation(t)

	// POST /products con branch_ids de una sucursal ajena: 403.
	if c, _ := prodReq(t, f.app, "s1", "POST", "/products", map[string]any{"name": "Nuevo", "branch_ids": []uint{2}}); c != 403 {
		t.Errorf("S1 creando producto con branch_ids=[2]: %d, quiere 403", c)
	}
	// PUT con branch_ids ajeno sobre un producto visible.
	if c, _ := prodReq(t, f.app, "s1", "PUT", fmt.Sprintf("/products/%d", f.all.ID), map[string]any{"name": "x", "branch_ids": []uint{2}}); c != 403 {
		t.Errorf("S1 asignando [2] por PUT: %d, quiere 403", c)
	}
	// bulk-branches: set a sucursal ajena -> 403; producto invisible -> 404.
	bulk := func(u string, ids []uint, branches []uint, mode string) int {
		c, _ := prodReq(t, f.app, u, "PATCH", "/products/bulk-branches", map[string]any{"product_ids": ids, "branch_ids": branches, "mode": mode})
		return c
	}
	if c := bulk("s1", []uint{f.all.ID}, []uint{2}, "set"); c != 403 {
		t.Errorf("S1 bulk set [2]: %d, quiere 403", c)
	}
	if c := bulk("s1", []uint{f.onlyS2.ID}, []uint{1}, "add"); c != 404 {
		t.Errorf("S1 bulk add sobre producto invisible: %d, quiere 404", c)
	}
	// Caso propio: S1 deja el producto [1,2] solo en [1]: la asignación a S2 (ajena) se conserva.
	if c := bulk("s1", []uint{f.both.ID}, []uint{1}, "set"); c != 200 {
		t.Fatalf("S1 bulk set [1] sobre su producto: %d, quiere 200", c)
	}
	var rows []database.TenantProductBranch
	f.db.Where("product_id = ?", f.both.ID).Order("branch_id").Find(&rows)
	if len(rows) != 2 || rows[0].BranchID != 1 || rows[1].BranchID != 2 {
		t.Errorf("la asignación a S2 (ajena) debía conservarse: %+v", rows)
	}
	// Dejarlo disponible en "todas" ([]) pisaría la asignación ajena: 403.
	if c := bulk("s1", []uint{f.both.ID}, []uint{}, "set"); c != 403 {
		t.Errorf("S1 bulk set [] con asignación ajena: %d, quiere 403", c)
	}
	// El administrador sí puede asignar cualquier sucursal.
	if c := bulk("admin", []uint{f.all.ID}, []uint{2, 3}, "set"); c != 200 {
		t.Errorf("admin bulk set [2,3]: %d, quiere 200", c)
	}
}

func TestProducts_BulkUpdatePricesPerBranch(t *testing.T) {
	f := setupProductIsolation(t)
	sale := 99.5
	body := func(branch uint) map[string]any {
		return map[string]any{"branch_id": branch, "rows": []map[string]any{{"row_number": 2, "code": "ALL", "sale_price": sale}}}
	}
	// Con sucursal: precio por sucursal, el global NO cambia.
	if c, resp := prodReq(t, f.app, "s1", "PATCH", "/products/bulk-update-prices", body(1)); c != 200 {
		t.Fatalf("S1 actualizando precio de su sucursal: %d %s", c, resp)
	}
	var global database.TenantProduct
	f.db.First(&global, f.all.ID)
	if global.SalePrice != 10 {
		t.Errorf("el precio GLOBAL no debía cambiar con sucursal indicada: %v", global.SalePrice)
	}
	var bp database.TenantProductBranchPrice
	if err := f.db.Where("product_id = ? AND branch_id = ?", f.all.ID, 1).First(&bp).Error; err != nil || bp.SalePrice != 99.5 || !bp.Active {
		t.Errorf("debía existir el precio de sucursal 99.5 activo: %+v err=%v", bp, err)
	}
	// Segunda corrida: actualiza (no duplica).
	sale = 80
	if c, _ := prodReq(t, f.app, "s1", "PATCH", "/products/bulk-update-prices", body(1)); c != 200 {
		t.Fatal("segunda actualización falló")
	}
	var n int64
	f.db.Model(&database.TenantProductBranchPrice{}).Where("product_id = ?", f.all.ID).Count(&n)
	f.db.Where("product_id = ? AND branch_id = ?", f.all.ID, 1).First(&bp)
	if n != 1 || bp.SalePrice != 80 {
		t.Errorf("debía haber 1 fila con 80: n=%d %+v", n, bp)
	}
	// Sucursal ajena -> 403 (no se acepta) y tampoco el admin escribe fuera de su activa.
	if c, _ := prodReq(t, f.app, "s1", "PATCH", "/products/bulk-update-prices", body(2)); c != 403 {
		t.Errorf("S1 con branch_id=2: %d, quiere 403", c)
	}
	if c, _ := prodReq(t, f.app, "admin", "PATCH", "/products/bulk-update-prices", body(2)); c != 403 {
		t.Errorf("admin con S1 activa y branch_id=2: %d, quiere 403", c)
	}
	// Producto no disponible en la sucursal: error por fila, sin precio guardado.
	onlyS2Body := map[string]any{"branch_id": 1, "rows": []map[string]any{{"row_number": 2, "code": "S2", "sale_price": 5.0}}}
	_, resp := prodReq(t, f.app, "s1", "PATCH", "/products/bulk-update-prices", onlyS2Body)
	var r struct {
		Data struct {
			Updated int `json:"updated"`
			Errors  int `json:"errors"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(resp), &r)
	if r.Data.Updated != 0 || r.Data.Errors != 1 {
		t.Errorf("producto solo de S2 desde S1: %s, quiere 0 actualizados y 1 error", resp)
	}
	// Sin sucursal: precio global (comportamiento histórico).
	globalBody := map[string]any{"rows": []map[string]any{{"row_number": 2, "code": "BOTH", "sale_price": 33.0}}}
	if c, _ := prodReq(t, f.app, "s1", "PATCH", "/products/bulk-update-prices", globalBody); c != 200 {
		t.Fatal("actualización global falló")
	}
	var both database.TenantProduct
	f.db.First(&both, f.both.ID)
	global = both
	if global.SalePrice != 33 {
		t.Errorf("sin sucursal debía actualizar el precio global: %v", global.SalePrice)
	}
	// Sin sucursal, un producto invisible para el usuario se reporta como inexistente.
	hidden := map[string]any{"rows": []map[string]any{{"row_number": 2, "code": "S2", "sale_price": 1.0}}}
	_, resp = prodReq(t, f.app, "s1", "PATCH", "/products/bulk-update-prices", hidden)
	_ = json.Unmarshal([]byte(resp), &r)
	if r.Data.Updated != 0 {
		t.Errorf("S1 no debe poder cambiar el precio global de un producto que no ve: %s", resp)
	}
	// Modo "todas" (sin sucursal activa): solo lectura.
	if c, _ := prodReq(t, f.app, "adminall", "PATCH", "/products/bulk-update-prices", body(1)); c != 403 {
		t.Errorf("admin sin sucursal activa: %d, quiere 403", c)
	}
}

func TestProducts_BranchPricesCRUDLimitedToAccessible(t *testing.T) {
	f := setupProductIsolation(t)
	base := fmt.Sprintf("/products/%d/branch-prices", f.all.ID)
	price := map[string]any{"sale_price": 12.5, "active": true}

	// Sucursal ajena: 404 para leer y escribir (vendedor S1).
	if c, _ := prodReq(t, f.app, "s1", "GET", base+"/2", nil); c != 404 {
		t.Errorf("S1 GET precio de S2: %d, quiere 404", c)
	}
	if c, _ := prodReq(t, f.app, "s1", "POST", base+"/2", price); c != 404 {
		t.Errorf("S1 POST precio de S2: %d, quiere 404", c)
	}
	if c, _ := prodReq(t, f.app, "s1", "PUT", base+"/2", price); c != 404 {
		t.Errorf("S1 PUT precio de S2: %d, quiere 404", c)
	}
	if c, _ := prodReq(t, f.app, "s1", "DELETE", base+"/2", nil); c != 404 {
		t.Errorf("S1 DELETE precio de S2: %d, quiere 404", c)
	}
	// El admin ve el precio de S2 pero no lo escribe con S1 activa.
	if err := f.db.Create(&database.TenantProductBranchPrice{ProductID: f.all.ID, BranchID: 2, SalePrice: 7, Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	if c, _ := prodReq(t, f.app, "admin", "GET", base+"/2", nil); c != 200 {
		t.Errorf("admin GET precio de S2: %d, quiere 200", c)
	}
	if c, _ := prodReq(t, f.app, "admin", "PUT", base+"/2", price); c != 403 {
		t.Errorf("admin con S1 activa PUT precio de S2: %d, quiere 403", c)
	}
	if c, _ := prodReq(t, f.app, "admin", "DELETE", base+"/2", nil); c != 403 {
		t.Errorf("admin con S1 activa DELETE precio de S2: %d, quiere 403", c)
	}
	// Listado: S1 no ve la fila de S2; el admin sí.
	count := func(u string) int {
		_, body := prodReq(t, f.app, u, "GET", base, nil)
		var out struct {
			Data []json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal([]byte(body), &out)
		return len(out.Data)
	}
	if n := count("s1"); n != 0 {
		t.Errorf("S1 ve %d precios por sucursal, quiere 0", n)
	}
	if n := count("admin"); n != 1 {
		t.Errorf("admin ve %d precios por sucursal, quiere 1", n)
	}
	// Caso propio: S1 crea, edita y borra el precio de su sucursal.
	if c, body := prodReq(t, f.app, "s1", "POST", base+"/1", price); c != 201 {
		t.Fatalf("S1 POST precio de S1: %d %s", c, body)
	}
	if c, _ := prodReq(t, f.app, "s1", "PUT", base+"/1", map[string]any{"sale_price": 14, "active": true}); c != 200 {
		t.Errorf("S1 PUT precio de S1: %d, quiere 200", c)
	}
	if c, _ := prodReq(t, f.app, "s1", "DELETE", base+"/1", nil); c != 200 {
		t.Errorf("S1 DELETE precio de S1: %d, quiere 200", c)
	}
	// Un producto invisible para S1 tampoco expone sus precios.
	if c, _ := prodReq(t, f.app, "s1", "GET", fmt.Sprintf("/products/%d/branch-prices", f.onlyS2.ID), nil); c != 404 {
		t.Errorf("S1 listando precios de producto invisible: %d, quiere 404", c)
	}
}
