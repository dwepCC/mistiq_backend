package summary

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"tukifac/pkg/database"
)

func memDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func mustExec(t *testing.T, db *gorm.DB, sqlText string, args ...any) {
	t.Helper()
	if err := db.Exec(sqlText, args...).Error; err != nil {
		t.Fatalf("%s: %v", sqlText, err)
	}
}

// tenantDB arma una BD de tenant mínima con las tablas que lee el escáner.
func tenantDB(t *testing.T, name string) *gorm.DB {
	db := memDB(t, name)
	mustExec(t, db, `CREATE TABLE tenant_document_series (id INTEGER PRIMARY KEY, sunat_code TEXT)`)
	mustExec(t, db, `CREATE TABLE tenant_sales (id INTEGER PRIMARY KEY AUTOINCREMENT, series_id INTEGER, issue_date DATETIME,
		total REAL, status TEXT, billing_status TEXT, deleted_at DATETIME)`)
	mustExec(t, db, `CREATE TABLE tenant_despatches (id INTEGER PRIMARY KEY AUTOINCREMENT, series_id INTEGER, issue_date DATETIME, status TEXT)`)
	for id, code := range map[int]string{1: "01", 2: "03", 3: "00", 4: "09", 5: "07"} {
		mustExec(t, db, `INSERT INTO tenant_document_series (id, sunat_code) VALUES (?, ?)`, id, code)
	}
	return db
}

func addSale(t *testing.T, db *gorm.DB, series int, day string, total float64, billing string, deleted bool) {
	t.Helper()
	del := any(nil)
	if deleted {
		del = day + " 12:00:00"
	}
	mustExec(t, db, `INSERT INTO tenant_sales (series_id, issue_date, total, status, billing_status, deleted_at) VALUES (?,?,?,?,?,?)`,
		series, day+" 10:00:00", total, "paid", billing, del)
}

func newScanner(t *testing.T, tdb *gorm.DB, now time.Time) (*Scanner, database.Tenant) {
	central := memDB(t, "central_"+t.Name())
	if err := central.AutoMigrate(&database.Tenant{}, &database.TenantFiscalDaily{}, &database.TenantFiscalHealth{}); err != nil {
		t.Fatal(err)
	}
	tenant := database.Tenant{Name: "Demo SAC", Slug: "demo", DBName: "demo_db", RUC: "20123456789", Status: "active"}
	if err := central.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	return &Scanner{
		Central: central,
		Open:    func(database.Tenant) (*gorm.DB, func(), error) { return tdb, func() {}, nil },
		Now:     func() time.Time { return now },
	}, tenant
}

func dailyCount(t *testing.T, s *Scanner, tenantID uint, day, code, st string) int {
	var r database.TenantFiscalDaily
	res := s.Central.Where("tenant_id = ? AND day = ? AND doc_code = ? AND billing_status = ?", tenantID, day, code, st).Limit(1).Find(&r)
	if res.RowsAffected == 0 {
		return 0
	}
	return r.Cnt
}

func TestScanTenantCountsElectronicOnlyAndSkipsNotasDeVentaYEliminadas(t *testing.T) {
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)
	tdb := tenantDB(t, "tenant_a")
	addSale(t, tdb, 1, "2026-10-05", 100, "accepted", false)
	addSale(t, tdb, 1, "2026-10-05", 50, "accepted", false)
	addSale(t, tdb, 2, "2026-10-05", 20, "pending", false)
	addSale(t, tdb, 1, "2026-10-05", 70, "error", false)
	addSale(t, tdb, 3, "2026-10-05", 999, "pending", false) // nota de venta (00): no cuenta
	addSale(t, tdb, 2, "2026-10-05", 10, "pending", true)   // eliminada: no cuenta
	addSale(t, tdb, 1, "2026-10-04", 30, "", false)         // billing_status vacío = pending
	mustExec(t, tdb, `INSERT INTO tenant_despatches (series_id, issue_date, status) VALUES (4, '2026-10-05 09:00:00', 'accepted'), (4, '2026-10-05 09:00:00', 'pending')`)

	s, tenant := newScanner(t, tdb, now)
	if err := s.ScanTenant(tenant, false); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		day, code, st string
		want          int
	}{
		{"2026-10-05", "01", "accepted", 2},
		{"2026-10-05", "03", "pending", 1}, // la eliminada no suma
		{"2026-10-05", "01", "error", 1},
		{"2026-10-04", "01", "pending", 1},
		{"2026-10-05", "09", "accepted", 1},
		{"2026-10-05", "09", "pending", 1},
		{"2026-10-05", "00", "pending", 0}, // nota de venta excluida
	} {
		if got := dailyCount(t, s, tenant.ID, c.day, c.code, c.st); got != c.want {
			t.Errorf("%s %s %s: got %d want %d", c.day, c.code, c.st, got, c.want)
		}
	}

	var h database.TenantFiscalHealth
	s.Central.First(&h, "tenant_id = ?", tenant.ID)
	if h.ScanError != "" {
		t.Fatalf("no debería haber error: %s", h.ScanError)
	}
	// pending: 03(1) + 01 del 04 (1) + guía (1) = 3; error: 1
	if h.OpenPending != 3 || h.OpenError != 1 {
		t.Errorf("abiertos incorrectos: pending=%d error=%d", h.OpenPending, h.OpenError)
	}
	if h.OldestOpenAt == nil || h.OldestOpenAt.Format("2006-01-02") != "2026-10-04" {
		t.Errorf("pendiente más antiguo incorrecto: %v", h.OldestOpenAt)
	}
	if h.LastIssueAt == nil || h.LastIssueAt.Format("2006-01-02") != "2026-10-05" {
		t.Errorf("último comprobante incorrecto: %v", h.LastIssueAt)
	}
}

func TestScanTenantActualizaDiferenciasYBorraLoQueYaNoExiste(t *testing.T) {
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)
	tdb := tenantDB(t, "tenant_b")
	addSale(t, tdb, 1, "2026-10-05", 100, "pending", false)
	s, tenant := newScanner(t, tdb, now)
	if err := s.ScanTenant(tenant, false); err != nil {
		t.Fatal(err)
	}
	if dailyCount(t, s, tenant.ID, "2026-10-05", "01", "pending") != 1 {
		t.Fatal("falta el pendiente inicial")
	}

	// El comprobante pasa a aceptado: la fila "pending" debe desaparecer, no quedar en 1.
	mustExec(t, tdb, `UPDATE tenant_sales SET billing_status = 'accepted'`)
	if err := s.ScanTenant(tenant, false); err != nil {
		t.Fatal(err)
	}
	if got := dailyCount(t, s, tenant.ID, "2026-10-05", "01", "pending"); got != 0 {
		t.Errorf("la fila pending debió borrarse, cnt=%d", got)
	}
	if got := dailyCount(t, s, tenant.ID, "2026-10-05", "01", "accepted"); got != 1 {
		t.Errorf("accepted debió ser 1, cnt=%d", got)
	}
	var n int64
	s.Central.Model(&database.TenantFiscalDaily{}).Where("tenant_id = ?", tenant.ID).Count(&n)
	if n != 1 {
		t.Errorf("debe quedar 1 sola fila, hay %d", n)
	}
}

func TestPasadaProfundaGuardaAbiertosAntiguosYElCicloCortoLosArrastra(t *testing.T) {
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)
	tdb := tenantDB(t, "tenant_c")
	addSale(t, tdb, 1, "2026-06-01", 100, "pending", false) // fuera de la ventana de 60 días
	addSale(t, tdb, 1, "2026-05-20", 100, "error", false)
	addSale(t, tdb, 1, "2026-10-05", 100, "pending", false)
	s, tenant := newScanner(t, tdb, now)

	// El ciclo corto, sin pasada profunda previa, solo ve la ventana.
	if err := s.ScanTenant(tenant, false); err != nil {
		t.Fatal(err)
	}
	var h database.TenantFiscalHealth
	s.Central.First(&h, "tenant_id = ?", tenant.ID)
	if h.OpenPending != 1 || h.OpenError != 0 {
		t.Fatalf("ciclo corto inicial: pending=%d error=%d", h.OpenPending, h.OpenError)
	}

	// La pasada profunda trae todo el historial.
	if err := s.ScanTenant(tenant, true); err != nil {
		t.Fatal(err)
	}
	s.Central.First(&h, "tenant_id = ?", tenant.ID)
	if h.OpenPending != 2 || h.OpenError != 1 {
		t.Fatalf("pasada profunda: pending=%d error=%d", h.OpenPending, h.OpenError)
	}
	if h.OlderOpenPending != 1 || h.OlderOpenError != 1 {
		t.Fatalf("older_*: pending=%d error=%d", h.OlderOpenPending, h.OlderOpenError)
	}
	if h.OldestOpenAt == nil || h.OldestOpenAt.Format("2006-01-02") != "2026-05-20" {
		t.Errorf("más antiguo: %v", h.OldestOpenAt)
	}

	// El siguiente ciclo corto NO pierde lo antiguo (lo arrastra) ni lo cuenta doble.
	if err := s.ScanTenant(tenant, false); err != nil {
		t.Fatal(err)
	}
	s.Central.First(&h, "tenant_id = ?", tenant.ID)
	if h.OpenPending != 2 || h.OpenError != 1 {
		t.Errorf("ciclo corto posterior: pending=%d error=%d", h.OpenPending, h.OpenError)
	}
	if h.LastDeepAt == nil {
		t.Error("debe conservar last_deep_at")
	}
	// Las filas antiguas de la pasada profunda no se borran en el ciclo corto.
	if dailyCount(t, s, tenant.ID, "2026-06-01", "01", "pending") != 1 {
		t.Error("el ciclo corto no debe tocar filas fuera de la ventana")
	}
}

func TestTenantConEsquemaViejoRegistraElErrorYConservaLoAnterior(t *testing.T) {
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)
	tdb := tenantDB(t, "tenant_d")
	addSale(t, tdb, 1, "2026-10-05", 100, "pending", false)
	s, tenant := newScanner(t, tdb, now)
	if err := s.ScanTenant(tenant, false); err != nil {
		t.Fatal(err)
	}

	mustExec(t, tdb, `DROP TABLE tenant_sales`)
	if err := s.ScanTenant(tenant, false); err == nil {
		t.Fatal("debía fallar sin tenant_sales")
	}
	var h database.TenantFiscalHealth
	s.Central.First(&h, "tenant_id = ?", tenant.ID)
	if h.ScanError == "" {
		t.Error("debe registrar scan_error")
	}
	if h.OpenPending != 1 {
		t.Errorf("debe conservar los conteos anteriores, open_pending=%d", h.OpenPending)
	}
}

func TestScanAllNoSeDetieneConUnTenantQueFalla(t *testing.T) {
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)
	good := tenantDB(t, "tenant_e_ok")
	addSale(t, good, 1, "2026-10-05", 100, "pending", false)
	s, ok := newScanner(t, good, now)
	bad := database.Tenant{Name: "Roto", Slug: "roto", DBName: "roto_db", Status: "suspended"}
	s.Central.Create(&bad)
	s.Open = func(tn database.Tenant) (*gorm.DB, func(), error) {
		if tn.ID == bad.ID {
			return nil, func() {}, fmt.Errorf("sin conexión")
		}
		return good, func() {}, nil
	}
	st, err := s.ScanAll(false, 2)
	if err != nil {
		t.Fatal(err)
	}
	if st.Tenants != 2 || st.Failed != 1 {
		t.Fatalf("stats: %+v", st)
	}
	if dailyCount(t, s, ok.ID, "2026-10-05", "01", "pending") != 1 {
		t.Error("el tenant sano debe quedar escaneado")
	}
}

func TestSummarizeFiltraPorFechaTipoRucYOrdena(t *testing.T) {
	central := memDB(t, "central_summarize")
	if err := central.AutoMigrate(&database.Tenant{}, &database.TenantFiscalDaily{}, &database.TenantFiscalHealth{}); err != nil {
		t.Fatal(err)
	}
	a := database.Tenant{Name: "Alfa SAC", Slug: "alfa", DBName: "a", RUC: "20111111111", Status: "active"}
	b := database.Tenant{Name: "Beta EIRL", Slug: "beta", DBName: "b", RUC: "20222222222", Status: "suspended"}
	c := database.Tenant{Name: "Gamma", Slug: "gamma", DBName: "c", RUC: "20333333333", Status: "active"}
	central.Create(&a)
	central.Create(&b)
	central.Create(&c)
	now := time.Now()
	add := func(tn database.Tenant, day, code, st string, n int) {
		central.Create(&database.TenantFiscalDaily{TenantID: tn.ID, Day: day, DocCode: code, BillingStatus: st, Cnt: n, RefreshedAt: now})
	}
	add(a, "2026-10-01", "01", "accepted", 10)
	add(a, "2026-10-02", "03", "pending", 3)
	add(a, "2026-09-15", "03", "pending", 7) // fuera del rango pedido
	add(b, "2026-10-02", "01", "error", 5)
	add(b, "2026-10-02", "09", "accepted", 2)
	central.Create(&database.TenantFiscalHealth{TenantID: a.ID, ScannedAt: now, OpenPending: 10, OpenError: 0})

	res, err := Summarize(central, Filter{From: "2026-10-01", To: "2026-10-31"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 {
		t.Fatalf("deben aparecer todos los tenants (también sin datos): %d", res.Total)
	}
	if res.Items[0].Slug != "beta" || res.Items[0].ToSend != 5 {
		t.Errorf("orden por 'faltan enviar': %+v", res.Items[0])
	}
	if res.Totals.Emitted != 20 || res.Totals.ToSend != 8 || res.Totals.WithToSend != 2 {
		t.Errorf("totales: %+v", res.Totals)
	}

	// Filtro por tipo: solo boletas.
	res, _ = Summarize(central, Filter{From: "2026-10-01", To: "2026-10-31", DocTypes: []string{"03"}})
	var alfa Row
	for _, it := range res.Items {
		if it.Slug == "alfa" {
			alfa = it
		}
	}
	if alfa.Emitted != 3 || alfa.ByType["03"].ToSend != 3 {
		t.Errorf("alfa solo boletas: %+v", alfa)
	}

	// Sin rango: suma también lo de septiembre.
	res, _ = Summarize(central, Filter{OnlyPending: true, Q: "alfa"})
	if res.Total != 1 || res.Items[0].ToSend != 10 {
		t.Errorf("sin rango + solo pendientes + búsqueda: %+v", res)
	}

	// RUC parcial y paginación.
	res, _ = Summarize(central, Filter{RUC: "2022", PerPage: 1})
	if res.Total != 1 || res.Items[0].Slug != "beta" {
		t.Errorf("filtro RUC: %+v", res)
	}
	res, _ = Summarize(central, Filter{PerPage: 2, Page: 2})
	if len(res.Items) != 1 || res.Total != 3 {
		t.Errorf("paginación: items=%d total=%d", len(res.Items), res.Total)
	}
}

func TestSummarizeMarcaPendientesAtrasadosYFiltra(t *testing.T) {
	central := memDB(t, "central_stale")
	if err := central.AutoMigrate(&database.Tenant{}, &database.TenantFiscalDaily{}, &database.TenantFiscalHealth{}); err != nil {
		t.Fatal(err)
	}
	a := database.Tenant{Name: "Atrasado", Slug: "atrasado", DBName: "a", Status: "active"}
	b := database.Tenant{Name: "Reciente", Slug: "reciente", DBName: "b", Status: "active"}
	c := database.Tenant{Name: "Sin pendientes", Slug: "limpio", DBName: "c", Status: "active"}
	central.Create(&a)
	central.Create(&b)
	central.Create(&c)
	now := time.Now()
	old := now.AddDate(0, 0, -(StaleAfterDays + 2))
	recent := now.AddDate(0, 0, -1)
	central.Create(&database.TenantFiscalHealth{TenantID: a.ID, ScannedAt: now, OpenPending: 4, OldestOpenAt: &old})
	central.Create(&database.TenantFiscalHealth{TenantID: b.ID, ScannedAt: now, OpenError: 2, OldestOpenAt: &recent})
	// Un OldestOpenAt viejo sin pendientes abiertos no es "atrasado".
	central.Create(&database.TenantFiscalHealth{TenantID: c.ID, ScannedAt: now, OldestOpenAt: &old})

	res, err := Summarize(central, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Totals.Stale != 1 {
		t.Fatalf("stale total=%d", res.Totals.Stale)
	}
	res, _ = Summarize(central, Filter{StaleOnly: true})
	if res.Total != 1 || res.Items[0].Slug != "atrasado" || !res.Items[0].Stale {
		t.Fatalf("stale_only: %+v", res)
	}
}
