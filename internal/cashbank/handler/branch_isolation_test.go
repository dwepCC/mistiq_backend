package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Fase 3A: aislamiento entre sucursales en caja. Dos sucursales (1 y 2) con una sesión abierta cada una.
//   - vendedor S1 (user 1, activa 1) y cajero S2 (user 2, activa 2) operan solo lo suyo;
//   - gerente S1 (user 3, cashbank.manage, activa 1) administra cualquier caja DE SU sucursal, no la de S2;
//   - admin (user 9, Administrador, ve todas, activa 1) VE la de S2 pero no escribe fuera de su activa.

type cbActor struct {
	user     uint
	active   uint
	allowed  []uint
	admin    bool
	manager  bool // permiso cashbank.manage
	roleName string
}

func (a cbActor) apply(req *http.Request) {
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
	if a.manager {
		req.Header.Set("X-Manager", "1")
	}
	req.Header.Set("X-Role", a.roleName)
}

var (
	vendedorS1 = cbActor{user: 1, active: 1, allowed: []uint{1}, roleName: "Vendedor"}
	cajeroS2   = cbActor{user: 2, active: 2, allowed: []uint{2}, roleName: "Cajero"}
	gerenteS1  = cbActor{user: 3, active: 1, allowed: []uint{1}, manager: true, roleName: "Gerente"}
	adminS1    = cbActor{user: 9, active: 1, allowed: []uint{1, 2}, admin: true, roleName: "Administrador"}
	adminS2    = cbActor{user: 9, active: 2, allowed: []uint{1, 2}, admin: true, roleName: "Administrador"}
)

func setupBranchIsolationDB(t *testing.T) (*gorm.DB, database.TenantCashSession, database.TenantCashSession) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantCashSession{}, &database.TenantCashMovement{}, &database.TenantBankMovement{},
		&database.TenantBankAccount{}, &database.TenantPaymentMethod{}, &database.TenantBranch{}, &database.TenantUser{}, &database.TenantContact{},
		&database.TenantPurchase{}, &database.TenantPurchasePayment{}, &database.TenantSale{}, &database.TenantSalePayment{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	s1 := database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 100}
	s2 := database.TenantCashSession{BranchID: 2, UserID: 2, OpenedBy: 2, Status: "open", OpeningBalance: 200}
	if err := db.Create(&s1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&s2).Error; err != nil {
		t.Fatal(err)
	}
	return db, s1, s2
}

func newCashbankBranchApp(db *gorm.DB) *fiber.App {
	h := NewCashBankHandler()
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
		claims := &middleware.TenantClaims{RoleName: c.Get("X-Role")}
		if c.Get("X-Manager") == "1" {
			claims.Permissions = []string{"cashbank.manage"}
		}
		c.Locals("tenant_claims", claims)
		return c.Next()
	})
	app.Get("/cashbank/sessions/open/list", h.ListOpenSessionsInBranchAPI)
	app.Get("/cashbank/sessions/:id", h.GetSessionAPI)
	app.Post("/cashbank/sessions/:id/close", h.CloseSessionAPI)
	app.Patch("/cashbank/sessions/:id/opening-balance", h.UpdateOpeningBalanceAPI)
	app.Delete("/cashbank/sessions/:id", h.DeleteSessionAPI)
	app.Post("/cashbank/sessions/:id/arqueo", h.SaveArqueoAPI)
	app.Get("/cashbank/sessions/:id/balance", h.GetSessionBalanceAPI)
	app.Get("/cashbank/sessions/:id/movements", h.GetMovementsAPI)
	app.Post("/cashbank/sessions/:id/movements", h.AddMovementAPI)
	app.Post("/cashbank/movements/:id/reverse", h.ReverseMovementAPI)
	app.Get("/cashbank/sessions/:id/report", h.GetSessionReportAPI)
	app.Get("/cashbank/reports/movements", h.ListMovementsReportAPI)
	return app
}

func doCB(t *testing.T, app *fiber.App, a cbActor, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	a.apply(req)
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

// Lecturas por ID: quien no ve la sucursal de la sesión recibe 404, aunque sea gerente.
func TestCashSession_ReadsAcrossBranches(t *testing.T) {
	db, s1, s2 := setupBranchIsolationDB(t)
	app := newCashbankBranchApp(db)
	reads := []string{"", "/balance", "/movements", "/report"}

	for _, suffix := range reads {
		p2 := fmt.Sprintf("/cashbank/sessions/%d%s", s2.ID, suffix)
		p1 := fmt.Sprintf("/cashbank/sessions/%d%s", s1.ID, suffix)
		if code, body := doCB(t, app, vendedorS1, "GET", p2, ""); code != 404 {
			t.Errorf("vendedor S1 leyendo sesión S2 %q: %d, quiere 404 (%s)", suffix, code, body)
		}
		if code, body := doCB(t, app, gerenteS1, "GET", p2, ""); code != 404 {
			t.Errorf("gerente S1 (cashbank.manage) leyendo sesión S2 %q: %d, quiere 404 (%s)", suffix, code, body)
		}
		if code, body := doCB(t, app, cajeroS2, "GET", p2, ""); code != 200 {
			t.Errorf("cajero S2 leyendo su sesión %q: %d, quiere 200 (%s)", suffix, code, body)
		}
		if code, body := doCB(t, app, adminS1, "GET", p2, ""); code != 200 {
			t.Errorf("admin (ve todas) leyendo sesión S2 %q: %d, quiere 200 (%s)", suffix, code, body)
		}
		if code, body := doCB(t, app, gerenteS1, "GET", p1, ""); code != 200 {
			t.Errorf("gerente S1 leyendo sesión S1 %q: %d, quiere 200 (%s)", suffix, code, body)
		}
	}
}

// Escrituras: 404 fuera de la sucursal visible, 403 para el admin que ve pero no es de su activa, OK en lo propio.
func TestCashSession_WritesAcrossBranches(t *testing.T) {
	db, s1, s2 := setupBranchIsolationDB(t)
	app := newCashbankBranchApp(db)

	type op struct{ name, method, path, body string }
	ops := func(id uint) []op {
		return []op{
			{"arqueo", "POST", fmt.Sprintf("/cashbank/sessions/%d/arqueo", id), `{"arqueo":{"100":1}}`},
			{"movimiento", "POST", fmt.Sprintf("/cashbank/sessions/%d/movements", id), `{"type":"income","payment_method":"cash","amount":10,"category":"x"}`},
			{"opening-balance", "PATCH", fmt.Sprintf("/cashbank/sessions/%d/opening-balance", id), `{"opening_balance":5}`},
			{"cierre", "POST", fmt.Sprintf("/cashbank/sessions/%d/close", id), `{"closing_balance":1}`},
			{"borrar", "DELETE", fmt.Sprintf("/cashbank/sessions/%d", id), ``},
		}
	}
	for _, o := range ops(s2.ID) {
		if code, body := doCB(t, app, vendedorS1, o.method, o.path, o.body); code != 404 {
			t.Errorf("%s: vendedor S1 sobre sesión S2: %d, quiere 404 (%s)", o.name, code, body)
		}
		if code, body := doCB(t, app, gerenteS1, o.method, o.path, o.body); code != 404 {
			t.Errorf("%s: gerente S1 sobre sesión S2: %d, quiere 404 (%s)", o.name, code, body)
		}
		if code, body := doCB(t, app, adminS1, o.method, o.path, o.body); code != 403 || !strings.Contains(body, "BRANCH_FORBIDDEN") {
			t.Errorf("%s: admin con activa S1 sobre sesión S2: %d, quiere 403 BRANCH_FORBIDDEN (%s)", o.name, code, body)
		}
	}
	// Nada se modificó en la sesión de S2.
	var after database.TenantCashSession
	if err := db.First(&after, s2.ID).Error; err != nil {
		t.Fatalf("la sesión S2 desapareció: %v", err)
	}
	if after.Status != "open" || after.OpeningBalance != 200 || after.ArqueoJSON != "" {
		t.Errorf("la sesión S2 fue alterada: %+v", after)
	}
	var movs int64
	db.Model(&database.TenantCashMovement{}).Where("cash_session_id = ?", s2.ID).Count(&movs)
	if movs != 0 {
		t.Errorf("se registraron %d movimientos en la sesión S2", movs)
	}

	// Caso propio: cajero S2 en su sesión funciona.
	if code, body := doCB(t, app, cajeroS2, "POST", fmt.Sprintf("/cashbank/sessions/%d/arqueo", s2.ID), `{"arqueo":{"100":1}}`); code != 200 {
		t.Errorf("arqueo propio: %d (%s)", code, body)
	}
	if code, body := doCB(t, app, cajeroS2, "POST", fmt.Sprintf("/cashbank/sessions/%d/movements", s2.ID),
		`{"type":"income","payment_method":"cash","amount":10,"category":"x"}`); code != 201 {
		t.Errorf("movimiento propio: %d (%s)", code, body)
	}
	if code, body := doCB(t, app, cajeroS2, "PATCH", fmt.Sprintf("/cashbank/sessions/%d/opening-balance", s2.ID), `{"opening_balance":250}`); code != 200 {
		t.Errorf("opening-balance propio: %d (%s)", code, body)
	}
	// Gerente en su propia sucursal cierra la caja de otro cajero de S1.
	if code, body := doCB(t, app, gerenteS1, "POST", fmt.Sprintf("/cashbank/sessions/%d/close", s1.ID), `{"closing_balance":100}`); code != 200 {
		t.Errorf("gerente cerrando caja de su sucursal: %d (%s)", code, body)
	}
	// Admin con activa S2 opera la de S2.
	if code, body := doCB(t, app, adminS2, "POST", fmt.Sprintf("/cashbank/sessions/%d/close", s2.ID), `{"closing_balance":260}`); code != 200 {
		t.Errorf("admin activa S2 cerrando caja S2: %d (%s)", code, body)
	}
}

// Reversión de movimientos: hereda la sucursal de la sesión.
func TestCashMovementReverse_AcrossBranches(t *testing.T) {
	db, _, s2 := setupBranchIsolationDB(t)
	mv := database.TenantCashMovement{CashSessionID: s2.ID, Type: "income", Amount: 10, PaymentMethod: "cash", Category: "manual", UserID: 2}
	if err := db.Create(&mv).Error; err != nil {
		t.Fatal(err)
	}
	app := newCashbankBranchApp(db)
	path := fmt.Sprintf("/cashbank/movements/%d/reverse", mv.ID)
	if code, body := doCB(t, app, gerenteS1, "POST", path, `{"kind":"cash"}`); code != 404 {
		t.Errorf("gerente S1 revirtiendo movimiento de S2: %d, quiere 404 (%s)", code, body)
	}
	if code, body := doCB(t, app, adminS1, "POST", path, `{"kind":"cash"}`); code != 403 {
		t.Errorf("admin activa S1 revirtiendo movimiento de S2: %d, quiere 403 (%s)", code, body)
	}
	var n int64
	db.Model(&database.TenantCashMovement{}).Count(&n)
	if n != 1 {
		t.Errorf("se creó un movimiento de reversión no permitido (total %d)", n)
	}
}

// GET /cashbank/sessions/open/list: el branch_id del cliente se valida.
func TestListOpenSessions_BranchIDValidated(t *testing.T) {
	db, s1, s2 := setupBranchIsolationDB(t)
	app := newCashbankBranchApp(db)

	ids := func(body string) []uint {
		var r struct {
			Data []struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatalf("json: %v (%s)", err, body)
		}
		var out []uint
		for _, d := range r.Data {
			out = append(out, d.ID)
		}
		return out
	}
	// Vendedor S1 pide la sucursal 2: cae a la suya.
	code, body := doCB(t, app, vendedorS1, "GET", "/cashbank/sessions/open/list?branch_id=2", "")
	got := ids(body)
	if code != 200 || len(got) != 1 || got[0] != s1.ID {
		t.Errorf("vendedor S1 con branch_id=2: %d %v, quiere solo la sesión %d", code, got, s1.ID)
	}
	// Admin sí puede pedir la 2.
	code, body = doCB(t, app, adminS1, "GET", "/cashbank/sessions/open/list?branch_id=2", "")
	got = ids(body)
	if code != 200 || len(got) != 1 || got[0] != s2.ID {
		t.Errorf("admin con branch_id=2: %d %v, quiere la sesión %d", code, got, s2.ID)
	}
	// Sin branch_id: la activa.
	code, body = doCB(t, app, cajeroS2, "GET", "/cashbank/sessions/open/list", "")
	got = ids(body)
	if code != 200 || len(got) != 1 || got[0] != s2.ID {
		t.Errorf("cajero S2 sin branch_id: %d %v, quiere la sesión %d", code, got, s2.ID)
	}
}

// Reporte de movimientos con session_id de otra sucursal: 404.
func TestMovementsReport_SessionOfOtherBranch(t *testing.T) {
	db, _, s2 := setupBranchIsolationDB(t)
	app := newCashbankBranchApp(db)
	path := fmt.Sprintf("/cashbank/reports/movements?session_id=%d", s2.ID)
	if code, body := doCB(t, app, gerenteS1, "GET", path, ""); code != 404 {
		t.Errorf("gerente S1 con session_id de S2: %d, quiere 404 (%s)", code, body)
	}
	if code, body := doCB(t, app, adminS1, "GET", path, ""); code != 200 {
		t.Errorf("admin con session_id de S2: %d, quiere 200 (%s)", code, body)
	}
}
