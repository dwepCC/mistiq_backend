package handler

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

var limaLoc = time.FixedZone("Lima", -5*3600)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, limaLoc) }

func TestComparisonWindowMonthInProgressUsesSameElapsedDays(t *testing.T) {
	// 6-oct: el mes de octubre completo elegido se compara con 1–6 de septiembre, no con todo septiembre.
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, limaLoc)
	w := comparisonWindow(day(2026, 10, 1), day(2026, 11, 1), now)
	if w.Mode != "month_to_date" {
		t.Fatalf("modo: %s", w.Mode)
	}
	if !w.From.Equal(day(2026, 9, 1)) || !w.ToExclusive.Equal(day(2026, 9, 7)) {
		t.Fatalf("ventana: %v – %v", w.From, w.ToExclusive)
	}
	if w.Label != "mismos 6 días del mes anterior" {
		t.Fatalf("etiqueta: %q", w.Label)
	}
}

func TestComparisonWindowCompletedMonthComparesAgainstTheWholePreviousMonth(t *testing.T) {
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, limaLoc)
	w := comparisonWindow(day(2026, 9, 1), day(2026, 10, 1), now)
	if !w.From.Equal(day(2026, 8, 1)) || !w.ToExclusive.Equal(day(2026, 8, 31)) {
		// agosto tiene 31 días y septiembre 30: se compara con los mismos 30 días
		t.Fatalf("ventana: %v – %v", w.From, w.ToExclusive)
	}
}

func TestComparisonWindowPreviousMonthWithFewerDaysIsCapped(t *testing.T) {
	// 31-mar vs febrero (28 días): no puede pasarse del fin de febrero.
	now := time.Date(2026, 3, 31, 10, 0, 0, 0, limaLoc)
	w := comparisonWindow(day(2026, 3, 1), day(2026, 4, 1), now)
	if !w.ToExclusive.Equal(day(2026, 3, 1)) {
		t.Fatalf("debe tope en el inicio del mes actual: %v", w.ToExclusive)
	}
}

func TestComparisonWindowArbitraryRangeUsesEqualLengthWindowBefore(t *testing.T) {
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, limaLoc)
	w := comparisonWindow(day(2026, 10, 3), day(2026, 10, 6), now)
	if w.Mode != "previous_period" || !w.From.Equal(day(2026, 9, 30)) || !w.ToExclusive.Equal(day(2026, 10, 3)) {
		t.Fatalf("ventana: %+v", w)
	}
}

func TestFillDailySeriesAddsZeroDaysUpToToday(t *testing.T) {
	now := time.Date(2026, 10, 5, 15, 0, 0, 0, limaLoc)
	rows := []dailyRow{
		{Day: "2026-10-01T00:00:00-05:00", Sales: 100, Docs: 3},
		{Day: "2026-10-04", Sales: 40, Docs: 1},
	}
	out := fillDailySeries(rows, day(2026, 10, 1), day(2026, 11, 1), now)
	if len(out) != 5 { // 1..5 de octubre; no se inventan días futuros
		t.Fatalf("días: %d %+v", len(out), out)
	}
	want := []struct {
		day   string
		sales float64
		docs  int64
	}{{"2026-10-01", 100, 3}, {"2026-10-02", 0, 0}, {"2026-10-03", 0, 0}, {"2026-10-04", 40, 1}, {"2026-10-05", 0, 0}}
	for i, w := range want {
		if out[i].Day != w.day || out[i].Sales != w.sales || out[i].Docs != w.docs {
			t.Errorf("fila %d: %+v, quiero %+v", i, out[i], w)
		}
	}
}

func TestFillDailySeriesPastRangeIsFilledToItsEnd(t *testing.T) {
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, limaLoc)
	out := fillDailySeries(nil, day(2026, 9, 28), day(2026, 10, 1), now)
	if len(out) != 3 || out[2].Day != "2026-09-30" {
		t.Fatalf("%+v", out)
	}
}

func scopeTestDB(t *testing.T, name string) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestQueryLowStockDistinguishesOutOfStockFromLowAndIgnoresNoMinimum(t *testing.T) {
	db := scopeTestDB(t, "lowstock_scope")
	for _, q := range []string{
		`CREATE TABLE tenant_products (id INTEGER PRIMARY KEY, name TEXT, min_stock REAL, manage_stock BOOLEAN, active BOOLEAN)`,
		`CREATE TABLE tenant_product_stocks (id INTEGER PRIMARY KEY AUTOINCREMENT, product_id INTEGER, branch_id INTEGER, quantity REAL)`,
		`INSERT INTO tenant_products VALUES (1,'Agotado sin mínimo',0,1,1),(2,'Bajo con mínimo',10,1,1),(3,'Sano con mínimo',10,1,1),(4,'No gestiona stock',0,0,1),(5,'Inactivo',5,1,0),(6,'Sano sin mínimo',0,1,1)`,
		`INSERT INTO tenant_product_stocks (product_id, branch_id, quantity) VALUES (1,1,0),(2,1,4),(3,1,50),(4,1,0),(5,1,0),(6,1,7),(2,2,100)`,
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows := queryLowStock(db, 0, 10)
	got := map[string]string{}
	for _, r := range rows {
		got[r.ProductName] = r.Status
	}
	if len(got) != 2 || got["Agotado sin mínimo"] != "out" || got["Bajo con mínimo"] != "low" {
		t.Fatalf("filas: %+v", rows)
	}

	// Por sucursal: en la 2 "Bajo con mínimo" tiene 100, no aparece.
	if r := queryLowStock(db, 2, 10); len(r) != 0 {
		t.Fatalf("sucursal 2: %+v", r)
	}
}

func TestCountElectronicBillingUsesSeriesCodesExcludesCancelledAndScopesUser(t *testing.T) {
	db := scopeTestDB(t, "billing_scope")
	for _, q := range []string{
		`CREATE TABLE tenant_document_series (id INTEGER PRIMARY KEY, sunat_code TEXT)`,
		`CREATE TABLE tenant_sales (id INTEGER PRIMARY KEY AUTOINCREMENT, series_id INTEGER, billing_status TEXT, status TEXT, branch_id INTEGER, user_id INTEGER, deleted_at DATETIME)`,
		`INSERT INTO tenant_document_series VALUES (1,'01'),(2,'03'),(3,'00'),(4,'07')`,
		// 1: boleta pendiente (u1) · 2: factura pendiente (u2) · 3: nota de venta pendiente (no electrónica)
		// 4: boleta pendiente ANULADA · 5: NC pendiente (u1) · 6: boleta aceptada
		`INSERT INTO tenant_sales (series_id, billing_status, status, branch_id, user_id) VALUES
			(2,'pending','paid',1,1),(1,'pending','paid',1,2),(3,'pending','paid',1,1),
			(2,'pending','cancelled',1,1),(4,'pending','paid',1,1),(2,'accepted','paid',1,1)`,
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	if n := countElectronicBilling(db, "pending", 0, 0, false); n != 3 { // boleta, factura y NC
		t.Fatalf("todos: %d", n)
	}
	if n := countElectronicBilling(db, "pending", 0, 1, true); n != 2 { // solo del usuario 1
		t.Fatalf("usuario 1: %d", n)
	}
	if n := countElectronicBilling(db, "accepted", 0, 0, false); n != 1 {
		t.Fatalf("aceptadas: %d", n)
	}
}

func resolveViaRequest(t *testing.T, admin bool, self uint, query string) dashUser {
	t.Helper()
	app := fiber.New()
	var got dashUser
	app.Get("/x", func(c fiber.Ctx) error {
		if self != 0 {
			c.Locals("user_id", self)
		}
		c.Locals("is_branch_admin", admin)
		got = resolveDashboardUser(c)
		return c.SendStatus(200)
	})
	req := httptest.NewRequest("GET", "/x"+query, nil)
	if _, err := app.Test(req); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestResolveDashboardUserAdminSeesAllOrPicksOne(t *testing.T) {
	u := resolveViaRequest(t, true, 1, "")
	if !u.IsAdmin || u.Restrict {
		t.Fatalf("admin sin filtro debe ver todo: %+v", u)
	}
	u = resolveViaRequest(t, true, 1, "?user_id=7")
	if !u.Restrict || u.UserID != 7 {
		t.Fatalf("admin con user_id: %+v", u)
	}
}

func TestResolveDashboardUserNonAdminIsAlwaysRestrictedToSelf(t *testing.T) {
	u := resolveViaRequest(t, false, 5, "")
	if u.IsAdmin || !u.Restrict || u.UserID != 5 {
		t.Fatalf("no admin: %+v", u)
	}
	// Mandar el user_id de otro NO sirve para ver sus datos.
	u = resolveViaRequest(t, false, 5, "?user_id=9")
	if u.UserID != 5 || !u.Restrict {
		t.Fatalf("un no admin no puede elegir usuario: %+v", u)
	}
}

func TestResolveDashboardUserWithoutIdentityNeverFallsBackToEveryone(t *testing.T) {
	u := resolveViaRequest(t, false, 0, "")
	if !u.Restrict || u.UserID == 0 {
		t.Fatalf("sin identidad debe quedar acotado a un id imposible: %+v", u)
	}
}

func TestSumManualExpensesCountsOnlyUnlinkedUnreversedGastos(t *testing.T) {
	db := scopeTestDB(t, "gastos_scope")
	for _, q := range []string{
		`CREATE TABLE tenant_cash_sessions (id INTEGER PRIMARY KEY, branch_id INTEGER, user_id INTEGER)`,
		`CREATE TABLE tenant_cash_movements (id INTEGER PRIMARY KEY AUTOINCREMENT, cash_session_id INTEGER, type TEXT, category TEXT,
			amount REAL, sale_id INTEGER, purchase_id INTEGER, reversal_of_id INTEGER, created_at DATETIME)`,
		`INSERT INTO tenant_cash_sessions VALUES (1,1,10),(2,2,20)`,
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	ins := func(sess int, typ, cat string, amt float64, sale, purchase, reversal any, at string) {
		t.Helper()
		if err := db.Exec(`INSERT INTO tenant_cash_movements (cash_session_id, type, category, amount, sale_id, purchase_id, reversal_of_id, created_at)
			VALUES (?,?,?,?,?,?,?,?)`, sess, typ, cat, amt, sale, purchase, reversal, at).Error; err != nil {
			t.Fatal(err)
		}
	}
	ins(1, "expense", "gasto", 100, nil, nil, nil, "2026-10-02 10:00:00")        // 1: cuenta
	ins(1, "expense", "Gasto", 50, nil, nil, nil, "2026-10-03 10:00:00")         // 2: cuenta (mayúscula)
	ins(1, "expense", "gasto", 30, nil, nil, nil, "2026-10-04 10:00:00")         // 3: se revierte (no cuenta)
	ins(1, "income", "gasto", 30, nil, nil, 3, "2026-10-04 11:00:00")            // 4: reversión de 3
	ins(1, "expense", "gasto", 70, 99, nil, nil, "2026-10-05 10:00:00")          // 5: ligado a una venta: no
	ins(1, "expense", "compra", 80, nil, 5, nil, "2026-10-05 10:00:00")          // 6: compra: no
	ins(1, "expense", "egreso_manual", 40, nil, nil, nil, "2026-10-05 10:00:00") // 7: no es "gasto"
	ins(2, "expense", "gasto", 25, nil, nil, nil, "2026-10-05 10:00:00")         // 8: otra sucursal/usuario
	ins(1, "expense", "gasto", 900, nil, nil, nil, "2026-09-15 10:00:00")        // 9: fuera del período

	from, to := day(2026, 10, 1), day(2026, 11, 1)
	if got := sumManualExpenses(db, from, to, 0, 0, false); got != 175 { // 100+50+25
		t.Fatalf("todos: %v", got)
	}
	if got := sumManualExpenses(db, from, to, 1, 0, false); got != 150 {
		t.Fatalf("sucursal 1: %v", got)
	}
	if got := sumManualExpenses(db, from, to, 0, 20, true); got != 25 {
		t.Fatalf("usuario 20: %v", got)
	}
}
