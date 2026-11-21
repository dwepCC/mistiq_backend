package handler

import (
	"encoding/json"
	"fmt"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantmigrations"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Categorías por sucursal (V192). Usuarios por cabecera X-U:
//
//	admin: administra el catálogo global, ve todas las sucursales (activa S1)
//	s1 / c2: sin products.manage_global, sucursal activa 1 / 2
func newCategoryBranchApp(db *gorm.DB) *fiber.App {
	h := &ProductHandler{}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", uint(1))
		switch c.Get("X-U") {
		case "admin":
			c.Locals("active_branch_id", uint(1))
			c.Locals("allowed_branch_ids", []uint{1})
			c.Locals("is_branch_admin", true)
			c.Locals("tenant_claims", &middleware.TenantClaims{RoleID: 1})
		case "s1":
			c.Locals("active_branch_id", uint(1))
			c.Locals("allowed_branch_ids", []uint{1})
			c.Locals("tenant_claims", &middleware.TenantClaims{RoleID: 2})
		case "c2":
			c.Locals("active_branch_id", uint(2))
			c.Locals("allowed_branch_ids", []uint{2})
			c.Locals("tenant_claims", &middleware.TenantClaims{RoleID: 2})
		}
		return c.Next()
	})
	app.Get("/categories", h.CategoryListAPI)
	app.Post("/categories", h.CategoryCreateAPI)
	app.Put("/categories/:id", h.CategoryUpdateAPI)
	app.Delete("/categories/:id", h.CategoryDeleteAPI)
	app.Put("/products/:id", h.UpdateAPI)
	return app
}

func setupCategoryBranch(t *testing.T) (*fiber.App, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := (tenantmigrations.V177ProductBranches{}).Up(db); err != nil {
		t.Fatal(err)
	}
	if err := (tenantmigrations.V207CategoryBranch{}).Up(db); err != nil {
		t.Fatal(err)
	}
	for i, n := range []string{"Principal", "Sucursal 2"} {
		if err := db.Create(&database.TenantBranch{Name: n, Active: true, IsMain: i == 0}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// products.manage_global sembrado: solo el rol 1 (administrador del catálogo) lo tiene.
	perm := database.TenantPermission{Module: "products", Action: "manage_global"}
	db.Create(&perm)
	db.Create(&database.TenantRolePermission{RoleID: 1, PermissionID: perm.ID})
	return newCategoryBranchApp(db), db
}

type catResp struct {
	Data struct {
		ID       uint   `json:"id"`
		Name     string `json:"name"`
		BranchID *uint  `json:"branch_id"`
	} `json:"data"`
	Error string `json:"error"`
}

func createCat(t *testing.T, app *fiber.App, user string, body map[string]any) (int, catResp) {
	t.Helper()
	code, raw := prodReq(t, app, user, "POST", "/categories", body)
	var r catResp
	_ = json.Unmarshal([]byte(raw), &r)
	return code, r
}

func listCatNames(t *testing.T, app *fiber.App, user, query string) string {
	t.Helper()
	_, raw := prodReq(t, app, user, "GET", "/categories"+query, nil)
	var r struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(raw), &r)
	out := ""
	for i, c := range r.Data {
		if i > 0 {
			out += ","
		}
		out += c.Name
	}
	return out
}

func TestCategoryBranch_AltaYListadoPorSucursal(t *testing.T) {
	app, _ := setupCategoryBranch(t)

	// Alta sin sucursal = todas (NULL). El administrador puede crear en cualquier sucursal.
	if code, r := createCat(t, app, "admin", map[string]any{"name": "Todas"}); code != 201 || r.Data.BranchID != nil {
		t.Fatalf("alta de todas: %d branch=%v err=%s", code, r.Data.BranchID, r.Error)
	}
	if code, r := createCat(t, app, "admin", map[string]any{"name": "Solo S1", "branch_id": 1}); code != 201 || r.Data.BranchID == nil || *r.Data.BranchID != 1 {
		t.Fatalf("alta de S1: %d %+v", code, r)
	}
	if code, _ := createCat(t, app, "admin", map[string]any{"name": "Solo S2", "branch_id": 2}); code != 201 {
		t.Fatalf("alta de S2: %d", code)
	}

	// Administrador sin filtro: ve las tres (vista de administración). Con branch_id: las de esa sucursal + todas.
	if got := listCatNames(t, app, "admin", ""); got != "Todas,Solo S1,Solo S2" {
		t.Errorf("admin sin filtro: %q", got)
	}
	if got := listCatNames(t, app, "admin", "?branch_id=2"); got != "Todas,Solo S2" {
		t.Errorf("admin con branch_id=2: %q", got)
	}
	// Quien no ve todas las sucursales: solo las de todas y las de la suya, aunque no mande branch_id.
	if got := listCatNames(t, app, "s1", ""); got != "Todas,Solo S1" {
		t.Errorf("S1: %q", got)
	}
	if got := listCatNames(t, app, "c2", ""); got != "Todas,Solo S2" {
		t.Errorf("S2: %q", got)
	}
	// Y no puede pedir otra sucursal que no es suya: cae a la suya (fail-closed).
	if got := listCatNames(t, app, "s1", "?branch_id=2"); got != "Todas,Solo S1" {
		t.Errorf("S1 pidiendo la sucursal 2: %q", got)
	}
}

func TestCategoryBranch_UsuarioRestringidoSoloGestionaLaSuya(t *testing.T) {
	app, _ := setupCategoryBranch(t)
	_, global := createCat(t, app, "admin", map[string]any{"name": "Todas"})
	_, s2 := createCat(t, app, "admin", map[string]any{"name": "Solo S2", "branch_id": 2})

	// Crear: ni «todas» ni la de otra sucursal; sí la suya.
	if code, _ := createCat(t, app, "s1", map[string]any{"name": "X"}); code != 403 {
		t.Errorf("S1 creando categoría de todas: %d, quiere 403", code)
	}
	if code, _ := createCat(t, app, "s1", map[string]any{"name": "X", "branch_id": 2}); code != 403 {
		t.Errorf("S1 creando categoría de la S2: %d, quiere 403", code)
	}
	code, own := createCat(t, app, "s1", map[string]any{"name": "Mia", "branch_id": 1})
	if code != 201 {
		t.Fatalf("S1 creando la suya: %d %s", code, own.Error)
	}
	// Editar / eliminar: solo las de su sucursal.
	put := func(u string, id uint, body map[string]any) int {
		c, _ := prodReq(t, app, u, "PUT", fmt.Sprintf("/categories/%d", id), body)
		return c
	}
	if c := put("s1", global.Data.ID, map[string]any{"name": "Todas", "sort_order": 3}); c != 403 {
		t.Errorf("S1 editando la de todas: %d, quiere 403", c)
	}
	if c := put("s1", s2.Data.ID, map[string]any{"name": "Solo S2", "sort_order": 3}); c != 403 {
		t.Errorf("S1 editando la de S2: %d, quiere 403", c)
	}
	if c := put("s1", own.Data.ID, map[string]any{"name": "Mia 2", "sort_order": 3}); c != 200 {
		t.Errorf("S1 editando la suya: %d, quiere 200", c)
	}
	// No puede ampliarla a todas (eso es del administrador del catálogo).
	if c := put("s1", own.Data.ID, map[string]any{"name": "Mia 2", "branch_id": nil}); c != 403 {
		t.Errorf("S1 ampliando su categoría a todas: %d, quiere 403", c)
	}
	if c, _ := prodReq(t, app, "s1", "DELETE", fmt.Sprintf("/categories/%d", global.Data.ID), nil); c != 403 {
		t.Errorf("S1 eliminando la de todas: %d, quiere 403", c)
	}
	if c, _ := prodReq(t, app, "s1", "DELETE", fmt.Sprintf("/categories/%d", own.Data.ID), nil); c != 200 {
		t.Errorf("S1 eliminando la suya: %d, quiere 200", c)
	}
	// El administrador sí puede ampliar/cambiar cualquiera.
	if c := put("admin", s2.Data.ID, map[string]any{"name": "Solo S2", "branch_id": nil}); c != 200 {
		t.Errorf("admin ampliando a todas: %d, quiere 200", c)
	}
}

// Producto ↔ categoría de sucursal: se rechaza al guardar si el producto no se vende solo en esa sucursal.
func TestCategoryBranch_ProductoDebeSerCompatible(t *testing.T) {
	app, db := setupCategoryBranch(t)
	_, s1cat := createCat(t, app, "admin", map[string]any{"name": "Solo S1", "branch_id": 1})
	p := database.TenantProduct{Code: "P1", Name: "Inca Kola", Type: "product", Unit: "NIU", SalePrice: 5, Active: true}
	db.Create(&p)
	put := func(body map[string]any) (int, string) {
		return prodReq(t, app, "admin", "PUT", fmt.Sprintf("/products/%d", p.ID), body)
	}
	// Producto de todas las sucursales (sin asignación) con categoría de la S1: rechazado.
	if c, body := put(map[string]any{"name": "Inca Kola", "category_id": s1cat.Data.ID}); c != 400 {
		t.Errorf("producto de todas con categoría de S1: %d (%s), quiere 400", c, body)
	}
	// También vendido en la S2: rechazado.
	if c, _ := put(map[string]any{"name": "Inca Kola", "category_id": s1cat.Data.ID, "branch_ids": []uint{1, 2}}); c != 400 {
		t.Errorf("producto en S1 y S2 con categoría de S1: %d, quiere 400", c)
	}
	// Solo en la S1: permitido.
	if c, body := put(map[string]any{"name": "Inca Kola", "category_id": s1cat.Data.ID, "branch_ids": []uint{1}}); c != 200 {
		t.Errorf("producto solo en S1 con categoría de S1: %d (%s), quiere 200", c, body)
	}
	// Con la categoría ya asignada, ampliar el producto a la S2 sin tocar la categoría también se rechaza.
	if c, _ := put(map[string]any{"name": "Inca Kola", "category_id": s1cat.Data.ID, "branch_ids": []uint{1, 2}}); c != 400 {
		t.Errorf("ampliar a S2 conservando la categoría de S1: %d, quiere 400", c)
	}
}

// Limitar una categoría con productos de otras sucursales se bloquea y dice cuántos.
func TestCategoryBranch_NoSePuedeLimitarSiHayProductosEnOtrasSucursales(t *testing.T) {
	app, db := setupCategoryBranch(t)
	_, c := createCat(t, app, "admin", map[string]any{"name": "Bebidas"})
	p := database.TenantProduct{Code: "P1", Name: "Chicha", Type: "product", Unit: "NIU", SalePrice: 5, Active: true, CategoryID: &c.Data.ID}
	db.Create(&p) // sin asignación = todas las sucursales
	code, raw := prodReq(t, app, "admin", "PUT", fmt.Sprintf("/categories/%d", c.Data.ID), map[string]any{"name": "Bebidas", "branch_id": 1})
	if code != 400 {
		t.Fatalf("limitar a S1 con un producto de todas: %d (%s), quiere 400", code, raw)
	}
}

// Plato de la carta de Tukichef (is_restaurant): es de la sucursal activa aunque no mande «Disponible en», así que admite la
// categoría de esa sucursal; con la categoría de otra, no.
func TestCategoryBranch_PlatoDeCartaEsDeLaSucursalActiva(t *testing.T) {
	app, _ := setupCategoryBranch(t)
	_, s1 := createCat(t, app, "admin", map[string]any{"name": "Carta S1", "branch_id": 1})
	_, s2 := createCat(t, app, "admin", map[string]any{"name": "Carta S2", "branch_id": 2})
	app.Post("/products", (&ProductHandler{}).CreateAPI)
	body := func(cat uint, code string) map[string]any {
		return map[string]any{"name": "Plato " + code, "code": code, "type": "product", "unit": "NIU", "sale_price": 10,
			"igv_affectation_type": "10", "is_restaurant": true, "category_id": cat}
	}
	if c, raw := prodReq(t, app, "admin", "POST", "/products", body(s1.Data.ID, "PL1")); c != 201 {
		t.Errorf("plato de S1 con categoría de S1 (sucursal activa S1): %d %s, quiere 201", c, raw)
	}
	if c, _ := prodReq(t, app, "admin", "POST", "/products", body(s2.Data.ID, "PL2")); c != 400 {
		t.Errorf("plato de S1 con categoría de S2: %d, quiere 400", c)
	}
}
