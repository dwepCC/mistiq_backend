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

// Usuarios por cabecera X-U: s1 (vendedor/gerente de S1), c2 (S2), admin (Administrador, S1 activa), adminall (Administrador sin activa).
func newCompanyIsolationApp(db *gorm.DB) *fiber.App {
	h := NewCompanyHandler()
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
	app.Get("/company/series", h.ListSeriesAPI)
	app.Post("/company/series", h.CreateSeriesAPI)
	app.Put("/company/series/:id", h.UpdateSeriesAPI)
	app.Put("/company/series/:id/default", h.SetDefaultSeriesAPI)
	app.Delete("/company/series/:id", h.DeleteSeriesAPI)
	app.Post("/company/branches", h.CreateBranchAPI)
	app.Put("/company/branches/:id", h.UpdateBranchAPI)
	app.Delete("/company/branches/:id", h.DeleteBranchAPI)
	app.Delete("/company/branches/:id/logo", h.DeleteBranchLogoAPI)
	return app
}

func compReq(t *testing.T, app *fiber.App, user, method, path string, body any) (int, string) {
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

type compIso struct {
	app            *fiber.App
	db             *gorm.DB
	serie1, serie2 database.TenantDocumentSeries
}

func setupCompanyIsolation(t *testing.T) compIso {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	for i, n := range []string{"Principal", "Sucursal 2"} {
		if err := db.Create(&database.TenantBranch{Name: n, Active: true, IsMain: i == 0}).Error; err != nil {
			t.Fatal(err)
		}
	}
	f := compIso{db: db, app: newCompanyIsolationApp(db)}
	f.serie1 = database.TenantDocumentSeries{BranchID: 1, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta", Series: "NV01", Correlative: 1, Active: true}
	f.serie2 = database.TenantDocumentSeries{BranchID: 2, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta", Series: "NV02", Correlative: 1, Active: true}
	for _, s := range []*database.TenantDocumentSeries{&f.serie1, &f.serie2} {
		if err := db.Create(s).Error; err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestCompanySeries_BranchIsolation(t *testing.T) {
	f := setupCompanyIsolation(t)

	// Listado: sin branch_id, quien no ve todo recibe solo su sucursal activa; con branch_id ajeno cae a la activa.
	listed := func(u, q string) map[uint]bool {
		code, body := compReq(t, f.app, u, "GET", "/company/series"+q, nil)
		if code != 200 {
			t.Fatalf("listado %s: %d %s", u, code, body)
		}
		var out struct {
			Data []struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal([]byte(body), &out)
		m := map[uint]bool{}
		for _, s := range out.Data {
			m[s.ID] = true
		}
		return m
	}
	if m := listed("s1", ""); !m[f.serie1.ID] || m[f.serie2.ID] {
		t.Errorf("S1 sin branch_id debe ver solo su serie: %v", m)
	}
	if m := listed("s1", "?branch_id=2"); !m[f.serie1.ID] || m[f.serie2.ID] {
		t.Errorf("S1 pidiendo branch_id=2 no debe ver las series de S2: %v", m)
	}
	if m := listed("c2", ""); m[f.serie1.ID] || !m[f.serie2.ID] {
		t.Errorf("S2 debe ver solo su serie: %v", m)
	}
	if m := listed("admin", ""); !m[f.serie1.ID] || !m[f.serie2.ID] {
		t.Errorf("admin sin branch_id (pantalla de configuración) debe ver todas: %v", m)
	}
	if m := listed("admin", "?branch_id=2"); m[f.serie1.ID] || !m[f.serie2.ID] {
		t.Errorf("admin con branch_id=2 debe ver solo S2: %v", m)
	}

	// Mutaciones por ID: serie de otra sucursal -> 404.
	other := fmt.Sprintf("/company/series/%d", f.serie2.ID)
	if c, b := compReq(t, f.app, "s1", "PUT", other, map[string]any{"series": "NV02", "active": true, "doc_type": "NOTA DE VENTA"}); c != 404 {
		t.Errorf("S1 PUT serie de S2: %d (%s), quiere 404", c, b)
	}
	if c, b := compReq(t, f.app, "s1", "PUT", other+"/default", nil); c != 404 {
		t.Errorf("S1 default serie de S2: %d (%s), quiere 404", c, b)
	}
	if c, b := compReq(t, f.app, "s1", "DELETE", other, nil); c != 404 {
		t.Errorf("S1 DELETE serie de S2: %d (%s), quiere 404", c, b)
	}
	// Crear en sucursal ajena -> 404; en la propia, 201.
	if c, b := compReq(t, f.app, "s1", "POST", "/company/series", map[string]any{"branch_id": 2, "doc_type": "NOTA DE VENTA", "series": "NV77"}); c != 404 {
		t.Errorf("S1 creando serie en S2: %d (%s), quiere 404", c, b)
	}
	if c, b := compReq(t, f.app, "s1", "POST", "/company/series", map[string]any{"branch_id": 1, "doc_type": "NOTA DE VENTA", "series": "NV78"}); c != 201 {
		t.Errorf("S1 creando serie en su sucursal: %d (%s), quiere 201", c, b)
	}
	// Sucursal inexistente (admin): 404 por no accesible? El admin accede a todo, así que debe rechazarse por inexistente (400).
	if c, b := compReq(t, f.app, "admin", "POST", "/company/series", map[string]any{"branch_id": 99, "doc_type": "NOTA DE VENTA", "series": "NV79"}); c != 400 {
		t.Errorf("admin creando serie en sucursal inexistente: %d (%s), quiere 400", c, b)
	}
	// Modo "todas": sin sucursal activa no se escribe.
	if c, b := compReq(t, f.app, "adminall", "DELETE", fmt.Sprintf("/company/series/%d", f.serie1.ID), nil); c != 403 {
		t.Errorf("admin sin sucursal activa borrando serie: %d (%s), quiere 403", c, b)
	}
	// Caso propio: edición de su serie.
	own := fmt.Sprintf("/company/series/%d", f.serie1.ID)
	if c, b := compReq(t, f.app, "s1", "PUT", own, map[string]any{"series": "NV01", "active": true, "doc_type": "NOTA DE VENTA"}); c != 200 {
		t.Errorf("S1 editando su serie: %d (%s), quiere 200", c, b)
	}
	// El administrador configura series de cualquier sucursal.
	if c, b := compReq(t, f.app, "admin", "PUT", other, map[string]any{"series": "NV02", "active": true, "doc_type": "NOTA DE VENTA"}); c != 200 {
		t.Errorf("admin editando serie de S2: %d (%s), quiere 200", c, b)
	}
}

func TestCompanyBranches_Mutations(t *testing.T) {
	f := setupCompanyIsolation(t)

	// Crear y eliminar sucursales afecta al tenant entero: solo quien ve todas.
	if c, b := compReq(t, f.app, "s1", "POST", "/company/branches", map[string]any{"name": "Nueva"}); c != 403 {
		t.Errorf("S1 creando sucursal: %d (%s), quiere 403", c, b)
	}
	if c, b := compReq(t, f.app, "s1", "DELETE", "/company/branches/2", nil); c != 403 {
		t.Errorf("S1 eliminando sucursal: %d (%s), quiere 403", c, b)
	}
	// Editar otra sucursal -> 404; la propia -> 200; no puede volverse la principal.
	if c, b := compReq(t, f.app, "s1", "PUT", "/company/branches/2", map[string]any{"name": "Hack"}); c != 404 {
		t.Errorf("S1 editando S2: %d (%s), quiere 404", c, b)
	}
	if c, b := compReq(t, f.app, "c2", "PUT", "/company/branches/2", map[string]any{"name": "Sucursal 2 bis", "is_main": true}); c != 403 {
		t.Errorf("S2 marcándose principal: %d (%s), quiere 403", c, b)
	}
	if c, b := compReq(t, f.app, "c2", "PUT", "/company/branches/2", map[string]any{"name": "Sucursal 2 bis"}); c != 200 {
		t.Errorf("S2 editando su sucursal: %d (%s), quiere 200", c, b)
	}
	if c, b := compReq(t, f.app, "s1", "DELETE", "/company/branches/2/logo", nil); c != 404 {
		t.Errorf("S1 quitando logo de S2: %d (%s), quiere 404", c, b)
	}
	// El administrador conserva el control; en modo "todas" no escribe.
	if c, b := compReq(t, f.app, "admin", "PUT", "/company/branches/2", map[string]any{"name": "Sucursal 2 ter"}); c != 200 {
		t.Errorf("admin editando S2: %d (%s), quiere 200", c, b)
	}
	if c, b := compReq(t, f.app, "adminall", "PUT", "/company/branches/2", map[string]any{"name": "x"}); c != 403 {
		t.Errorf("admin sin sucursal activa editando: %d (%s), quiere 403", c, b)
	}
}
