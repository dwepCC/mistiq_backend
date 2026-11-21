package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"

	"tukifac/config"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Escenario (Fase 3A): sucursales 1 y 2. Usuarios por cabecera X-U:
//
//	s1: mesero de S1   c2: cajero de S2   admin: Administrador (ve todo) con S1 activa   adminall: Administrador sin sucursal activa
func newRestaurantIsolationApp(db *gorm.DB) *fiber.App {
	h := New()
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
	app.Put("/floors/:id", h.UpdateFloor)
	app.Delete("/floors/:id", h.DeleteFloor)
	app.Put("/tables/:id", h.UpdateTable)
	app.Delete("/tables/:id", h.DeleteTable)
	app.Get("/tables/:id/session", h.GetTableSession)
	app.Get("/sessions/:id", h.GetSession)
	app.Patch("/sessions/:id", h.UpdateSession)
	app.Patch("/sessions/:id/table", h.MoveSessionTable)
	app.Put("/sessions/:id/order-status", h.UpdateOrderStatus)
	app.Get("/sessions/:id/precuenta", h.GetPrecuenta)
	app.Post("/sessions/:id/orders", h.AddOrder)
	app.Post("/sessions/:id/bill", h.BillSession)
	app.Post("/sessions/:id/close", h.CloseSession)
	app.Post("/sessions/:id/cancel", h.CancelSession)
	app.Post("/sessions/:id/cancel-comandas", h.CancelAllComandas)
	app.Post("/table-orders/:id/printed", h.MarkTableOrderPrinted)
	app.Patch("/comandas/:id/notes", h.UpdateComandaNotes)
	app.Put("/comandas/:id/status", h.UpdateComandaStatus)
	app.Post("/comandas/:id/print", h.PrintComanda)
	app.Delete("/comandas/:id", h.CancelComanda)
	app.Post("/pos/checkout", h.POSCheckout)
	return app
}

func restReq(t *testing.T, app *fiber.App, user, method, path string, body any) (int, string) {
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

type restIso struct {
	app *fiber.App
	db  *gorm.DB
	// IDs por sucursal: [0] = S1, [1] = S2.
	floor, table, session, order, comanda [2]uint
}

func setupRestaurantIsolation(t *testing.T) restIso {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	f := restIso{db: db, app: newRestaurantIsolationApp(db)}
	for i := 0; i < 2; i++ {
		bid := uint(i + 1)
		if err := db.Create(&database.TenantBranch{Name: fmt.Sprintf("Sucursal %d", bid), Active: true, IsMain: i == 0}).Error; err != nil {
			t.Fatal(err)
		}
		fl := database.TenantRestaurantFloor{BranchID: bid, Name: fmt.Sprintf("Piso %d", bid), Active: true}
		if err := db.Create(&fl).Error; err != nil {
			t.Fatal(err)
		}
		tb := database.TenantRestaurantTable{BranchID: bid, FloorID: fl.ID, Name: fmt.Sprintf("Mesa %d", bid), Capacity: 4, Status: "ocupada", Active: true}
		if err := db.Create(&tb).Error; err != nil {
			t.Fatal(err)
		}
		tid := tb.ID
		ss := database.TenantTableSession{TableID: &tid, UserID: 1, BranchID: bid, Guests: 1, Status: "open", OrderType: "dine_in", OrderStatus: "pending"}
		if err := db.Create(&ss).Error; err != nil {
			t.Fatal(err)
		}
		ord := database.TenantTableOrder{SessionID: ss.ID, UserID: 1, OrderNumber: 1, Status: "active"}
		if err := db.Create(&ord).Error; err != nil {
			t.Fatal(err)
		}
		cm := database.TenantComanda{OrderID: ord.ID, SessionID: ss.ID, ProductName: "Plato", Quantity: 1, UnitPrice: 10}
		if err := db.Create(&cm).Error; err != nil {
			t.Fatal(err)
		}
		f.floor[i], f.table[i], f.session[i], f.order[i], f.comanda[i] = fl.ID, tb.ID, ss.ID, ord.ID, cm.ID
	}
	return f
}

// Cada fila: método, plantilla de ruta y cuerpo. {id} se reemplaza por el ID del registro de la sucursal que corresponda.
type restCase struct {
	name, method, tmpl string
	kind               string // floor | table | session | order | comanda
	write              bool
	body               any
}

func (f restIso) idFor(kind string, branchIdx int) uint {
	switch kind {
	case "floor":
		return f.floor[branchIdx]
	case "table":
		return f.table[branchIdx]
	case "session":
		return f.session[branchIdx]
	case "order":
		return f.order[branchIdx]
	default:
		return f.comanda[branchIdx]
	}
}

func restCases() []restCase {
	pin := map[string]any{"reason": "x", "pin": "1234"}
	return []restCase{
		{"PUT floor", "PUT", "/floors/%d", "floor", true, map[string]any{"name": "N", "sort_order": 1}},
		{"DELETE floor", "DELETE", "/floors/%d", "floor", true, nil},
		{"PUT table", "PUT", "/tables/%d", "table", true, map[string]any{"name": "N", "capacity": 2}},
		{"DELETE table", "DELETE", "/tables/%d", "table", true, nil},
		{"GET table session", "GET", "/tables/%d/session", "table", false, nil},
		{"GET session", "GET", "/sessions/%d", "session", false, nil},
		{"PATCH session", "PATCH", "/sessions/%d", "session", true, map[string]any{"notes": "x"}},
		{"PATCH session table", "PATCH", "/sessions/%d/table", "session", true, map[string]any{"table_id": 1}},
		{"PUT order-status", "PUT", "/sessions/%d/order-status", "session", true, map[string]any{"order_status": "pending"}},
		{"GET precuenta", "GET", "/sessions/%d/precuenta", "session", false, nil},
		{"POST orders", "POST", "/sessions/%d/orders", "session", true, map[string]any{"items": []any{}}},
		{"POST bill", "POST", "/sessions/%d/bill", "session", true, map[string]any{}},
		{"POST close", "POST", "/sessions/%d/close", "session", true, nil},
		{"POST cancel", "POST", "/sessions/%d/cancel", "session", true, pin},
		{"POST cancel-comandas", "POST", "/sessions/%d/cancel-comandas", "session", true, pin},
		{"POST table-orders printed", "POST", "/table-orders/%d/printed", "order", true, nil},
		{"PATCH comanda notes", "PATCH", "/comandas/%d/notes", "comanda", true, map[string]any{"notes": "x"}},
		{"PUT comanda status", "PUT", "/comandas/%d/status", "comanda", true, map[string]any{"status": "preparing"}},
		{"POST comanda print", "POST", "/comandas/%d/print", "comanda", true, nil},
		{"DELETE comanda", "DELETE", "/comandas/%d", "comanda", true, pin},
	}
}

// TestRestaurant_CrossBranchByID: cada endpoint por ID responde 404 al usuario de otra sucursal, el admin (ve todo)
// puede leer pero no modificar con otra sucursal activa (403), y el caso propio atraviesa la guarda.
func TestRestaurant_CrossBranchByID(t *testing.T) {
	for _, tc := range restCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := setupRestaurantIsolation(t)
			path := func(idx int) string { return fmt.Sprintf(tc.tmpl, f.idFor(tc.kind, idx)) }

			// Usuario de S1 sobre el registro de S2 -> 404; y el de S2 sobre el de S1.
			if c, b := restReq(t, f.app, "s1", tc.method, path(1), tc.body); c != 404 {
				t.Errorf("S1 sobre registro de S2: %d (%s), quiere 404", c, b)
			}
			if c, b := restReq(t, f.app, "c2", tc.method, path(0), tc.body); c != 404 {
				t.Errorf("S2 sobre registro de S1: %d (%s), quiere 404", c, b)
			}
			// Admin con S1 activa sobre S2: lectura pasa la guarda; escritura -> 403.
			c, b := restReq(t, f.app, "admin", tc.method, path(1), tc.body)
			if tc.write && c != 403 {
				t.Errorf("admin con S1 activa modificando registro de S2: %d (%s), quiere 403", c, b)
			}
			if !tc.write && (c == 404 || c == 403) {
				t.Errorf("admin con S1 activa leyendo registro de S2: %d (%s), la guarda no debe bloquear la lectura", c, b)
			}
			// Modo "todas" (sin sucursal activa): solo lectura.
			if tc.write {
				if c, b := restReq(t, f.app, "adminall", tc.method, path(0), tc.body); c != 403 {
					t.Errorf("admin sin sucursal activa modificando: %d (%s), quiere 403", c, b)
				}
			}
			// Caso propio: la guarda no bloquea (el servicio puede responder 200/400 según el cuerpo mínimo).
			if c, b := restReq(t, f.app, "s1", tc.method, path(0), tc.body); c == 404 && !tc.write && tc.kind != "table" {
				t.Errorf("S1 sobre su propio registro: %d (%s), no debe ser 404", c, b)
			} else if c == 403 {
				t.Errorf("S1 sobre su propio registro: 403 (%s), la guarda no debe bloquearlo", b)
			}
		})
	}
}

func TestRestaurant_POSCheckoutForeignSession(t *testing.T) {
	prev := config.AppConfig
	config.AppConfig = &config.Config{POSFastCheckoutEnabled: true}
	defer func() { config.AppConfig = prev }()

	f := setupRestaurantIsolation(t)
	body := func(sessionID uint) map[string]any {
		return map[string]any{
			"session_id": sessionID, "series_id": 1,
			"payments": []map[string]any{{"payment_method": "cash", "amount": 10}},
		}
	}
	// session_id de otra sucursal: 404 antes de tocar nada.
	if c, b := restReq(t, f.app, "s1", "POST", "/pos/checkout", body(f.session[1])); c != 404 {
		t.Errorf("S1 cobrando sesión de S2 por pos/checkout: %d (%s), quiere 404", c, b)
	}
	if c, b := restReq(t, f.app, "admin", "POST", "/pos/checkout", body(f.session[1])); c != 403 {
		t.Errorf("admin con S1 activa cobrando sesión de S2: %d (%s), quiere 403", c, b)
	}
	// Sesión propia: la guarda deja pasar (el servicio rechaza después por la serie inexistente: 400, no 404/403).
	if c, b := restReq(t, f.app, "s1", "POST", "/pos/checkout", body(f.session[0])); c == 404 || c == 403 {
		t.Errorf("S1 cobrando su propia sesión: %d (%s), la guarda no debe bloquearlo", c, b)
	}
}
