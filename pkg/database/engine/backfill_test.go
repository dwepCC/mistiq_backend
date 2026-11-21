package engine

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantbackfills"
	"tukifac/pkg/logger"
)

// fakeRunOnceBackfill simula un backfill normal (no repeatable) para probar shouldSkipBackfill
// sin depender de un TenantBackfill real registrado.
type fakeRunOnceBackfill struct{ version int }

func (f fakeRunOnceBackfill) Version() int        { return f.version }
func (fakeRunOnceBackfill) Name() string          { return "fake_run_once" }
func (fakeRunOnceBackfill) Run(db *gorm.DB) error { return nil }

// fakeRepeatableBackfill simula uno declarado tenantbackfills.Repeatable.
type fakeRepeatableBackfill struct{ version int }

func (f fakeRepeatableBackfill) Version() int        { return f.version }
func (fakeRepeatableBackfill) Name() string          { return "fake_repeatable" }
func (fakeRepeatableBackfill) Run(db *gorm.DB) error { return nil }
func (fakeRepeatableBackfill) Repeatable() bool      { return true }

func insertBackfillHistory(t *testing.T, db *gorm.DB, version int, success bool) {
	t.Helper()
	cs := "chk"
	if err := db.Create(&database.TenantMigrationHistory{
		Version: version, Name: "x", Type: database.MigrationHistoryTypeBackfill,
		Success: success, Checksum: &cs,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestShouldSkipBackfill_RunOnce_SkipsWhenAlreadyApplied(t *testing.T) {
	db := testTenantDB(t)
	insertBackfillHistory(t, db, 999, true)

	skip, err := shouldSkipBackfill(db, fakeRunOnceBackfill{version: 999})
	if err != nil {
		t.Fatal(err)
	}
	if !skip {
		t.Fatal("un backfill run-once ya aplicado con éxito debe saltarse")
	}
}

func TestShouldSkipBackfill_RunOnce_RunsWhenNotYetApplied(t *testing.T) {
	db := testTenantDB(t)

	skip, err := shouldSkipBackfill(db, fakeRunOnceBackfill{version: 999})
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("sin historial previo, un backfill run-once no debe saltarse")
	}
}

func TestShouldSkipBackfill_RunOnce_RunsAgainAfterPriorFailure(t *testing.T) {
	db := testTenantDB(t)
	insertBackfillHistory(t, db, 999, false)

	skip, err := shouldSkipBackfill(db, fakeRunOnceBackfill{version: 999})
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("un intento previo fallido (success=false) no debe bloquear el reintento")
	}
}

func TestShouldSkipBackfill_RunOnce_IgnoresLaterSchemaMigrations(t *testing.T) {
	db := testTenantDB(t)
	insertBackfillHistory(t, db, 999, true)
	insertHistory(t, db, 200, "later_schema", true)

	skip, err := shouldSkipBackfill(db, fakeRunOnceBackfill{version: 999})
	if err != nil {
		t.Fatal(err)
	}
	if !skip {
		t.Fatal("un backfill run-once aplicado no se repite por migraciones de schema posteriores")
	}
}

func TestShouldSkipBackfill_Repeatable_RerunsAfterSchemaChange(t *testing.T) {
	db := testTenantDB(t)
	insertHistory(t, db, 100, "s100", true)
	insertBackfillHistory(t, db, 999, true)
	insertHistory(t, db, 101, "s101", true)

	skip, err := shouldSkipBackfill(db, fakeRepeatableBackfill{version: 999})
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("un backfill Repeatable con un éxito ANTERIOR a la última migración de schema " +
			"debe volver a correr — ese éxito pudo ser 'vacío' por schema a medio desplegar, " +
			"justo el bug que Repeatable existe para evitar")
	}
}

func TestShouldSkipBackfill_Repeatable_SkipsWhileSchemaUnchanged(t *testing.T) {
	db := testTenantDB(t)
	insertHistory(t, db, 100, "s100", true)
	insertBackfillHistory(t, db, 999, true)
	insertHistory(t, db, 101, "s101_fallida", false)

	skip, err := shouldSkipBackfill(db, fakeRepeatableBackfill{version: 999})
	if err != nil {
		t.Fatal(err)
	}
	if !skip {
		t.Fatal("con un éxito posterior a la última migración de schema exitosa, un Repeatable " +
			"no debe re-ejecutarse en cada corrida del cron (antes: cada 5 min sin fin)")
	}
}

func TestShouldSkipBackfill_Repeatable_RunsWhenNeverSucceeded(t *testing.T) {
	db := testTenantDB(t)
	insertHistory(t, db, 100, "s100", true)
	insertBackfillHistory(t, db, 999, false)

	skip, err := shouldSkipBackfill(db, fakeRepeatableBackfill{version: 999})
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("un Repeatable sin ningún éxito registrado debe correr")
	}
}

func TestShouldSkipBackfill_RealV036IsWiredAsRepeatable(t *testing.T) {
	db := testTenantDB(t)
	bf := tenantbackfills.V036SalePaymentCashSessionBackfill{}
	insertBackfillHistory(t, db, bf.Version(), true)
	insertHistory(t, db, 123, "v123", true)

	skip, err := shouldSkipBackfill(db, bf)
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("V036SalePaymentCashSessionBackfill debe volver a correr tras una migración de " +
			"schema posterior a su último éxito")
	}
}

// --- runBackfillsOverTenants: presupuesto y rotación ---

// fleetFixture N tenants con su propia BD sqlite en memoria y un registro de qué backfill corrió
// en cuál tenant.
type fleetFixture struct {
	tenants []database.Tenant
	dbs     map[string]*gorm.DB

	mu   sync.Mutex
	runs map[int][]string // versión → slugs donde corrió
}

func newFleetFixture(t *testing.T, n int) *fleetFixture {
	t.Helper()
	if logger.L == nil {
		logger.L = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	f := &fleetFixture{dbs: map[string]*gorm.DB{}, runs: map[int][]string{}}
	for i := 0; i < n; i++ {
		slug := fmt.Sprintf("t%03d", i)
		dsn := fmt.Sprintf("file:%s_%s?mode=memory&cache=shared", t.Name(), slug)
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		// Cerrar libera la BD en memoria compartida: sin esto, `go test -count=N` reutiliza el
		// historial de la repetición anterior.
		t.Cleanup(func() { database.CloseTenantDB(db) })
		if err := db.AutoMigrate(&database.TenantMigrationHistory{}); err != nil {
			t.Fatal(err)
		}
		f.tenants = append(f.tenants, database.Tenant{Slug: slug, DBName: "db_" + slug})
		f.dbs["db_"+slug] = db
	}
	return f
}

func (f *fleetFixture) open(dbName string) (*gorm.DB, func(), error) {
	db, ok := f.dbs[dbName]
	if !ok {
		return nil, nil, errors.New("no existe " + dbName)
	}
	return db, func() {}, nil
}

func (f *fleetFixture) ranOn(version int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.runs[version]...)
	sort.Strings(out)
	return out
}

// recordingBackfill backfill de prueba que anota en qué tenant corrió (por el slug guardado en
// la propia BD de la fixture).
type recordingBackfill struct {
	version    int
	repeatable bool
	fail       bool
	f          *fleetFixture
}

func (b recordingBackfill) Version() int     { return b.version }
func (b recordingBackfill) Name() string     { return fmt.Sprintf("rec_%d", b.version) }
func (b recordingBackfill) Repeatable() bool { return b.repeatable }
func (b recordingBackfill) Run(db *gorm.DB) error {
	for name, d := range b.f.dbs {
		if d == db {
			b.f.mu.Lock()
			b.f.runs[b.version] = append(b.f.runs[b.version], name[len("db_"):])
			b.f.mu.Unlock()
		}
	}
	if b.fail {
		return errors.New("falla simulada")
	}
	return nil
}

func TestRunBackfillsOverTenants_LimitCountsOnlyRealRuns(t *testing.T) {
	f := newFleetFixture(t, 10)
	// Los 6 primeros ya lo tienen aplicado: antes, limit=3 truncaba a t000..t002 (todos skip) y
	// t006..t009 nunca lo recibían.
	for _, tn := range f.tenants[:6] {
		insertBackfillHistory(t, f.dbs[tn.DBName], 50, true)
	}
	bf := recordingBackfill{version: 50, f: f}

	runBackfillsOverTenants(f.tenants, []tenantbackfills.TenantBackfill{bf}, 3, 1, f.open)
	if got := f.ranOn(50); fmt.Sprint(got) != "[t006 t007 t008]" {
		t.Fatalf("primera corrida: corrió en %v, se esperaba [t006 t007 t008]", got)
	}

	runBackfillsOverTenants(f.tenants, []tenantbackfills.TenantBackfill{bf}, 3, 1, f.open)
	if got := f.ranOn(50); fmt.Sprint(got) != "[t006 t007 t008 t009]" {
		t.Fatalf("la segunda corrida debe avanzar al resto: corrió en %v", got)
	}

	runBackfillsOverTenants(f.tenants, []tenantbackfills.TenantBackfill{bf}, 3, 1, f.open)
	if got := f.ranOn(50); len(got) != 4 {
		t.Fatalf("con toda la flota al día no debe volver a correr: %v", got)
	}
}

func TestRunBackfillsOverTenants_RepeatableStopsOnceCurrentAndRotates(t *testing.T) {
	f := newFleetFixture(t, 5)
	for _, tn := range f.tenants {
		insertHistory(t, f.dbs[tn.DBName], 100, "s100", true)
	}
	bf := recordingBackfill{version: 36, repeatable: true, f: f}
	bfs := []tenantbackfills.TenantBackfill{bf}

	runBackfillsOverTenants(f.tenants, bfs, 2, 1, f.open)
	runBackfillsOverTenants(f.tenants, bfs, 2, 1, f.open)
	runBackfillsOverTenants(f.tenants, bfs, 2, 1, f.open)
	if got := f.ranOn(36); fmt.Sprint(got) != "[t000 t001 t002 t003 t004]" {
		t.Fatalf("3 corridas con limit=2 deben cubrir los 5 tenants una vez cada uno: %v", got)
	}

	runBackfillsOverTenants(f.tenants, bfs, 2, 1, f.open)
	if got := f.ranOn(36); len(got) != 5 {
		t.Fatalf("sin cambios de schema un Repeatable no se repite: %v", got)
	}

	// Una migración nueva en t003 lo vuelve a habilitar solo ahí.
	insertHistory(t, f.dbs["db_t003"], 101, "s101", true)
	runBackfillsOverTenants(f.tenants, bfs, 2, 1, f.open)
	if got := f.ranOn(36); fmt.Sprint(got) != "[t000 t001 t002 t003 t003 t004]" {
		t.Fatalf("tras migrar t003 debe re-correr solo en t003: %v", got)
	}
}

func TestRunBackfillsOverTenants_IndependentBudgetPerBackfill(t *testing.T) {
	f := newFleetFixture(t, 4)
	a := recordingBackfill{version: 60, f: f}
	b := recordingBackfill{version: 61, f: f}

	runBackfillsOverTenants(f.tenants, []tenantbackfills.TenantBackfill{a, b}, 2, 2, f.open)
	if got := f.ranOn(60); len(got) != 2 {
		t.Fatalf("backfill 60: %v", got)
	}
	if got := f.ranOn(61); len(got) != 2 {
		t.Fatalf("el presupuesto del 60 no debe consumir el del 61: %v", got)
	}
}

func TestRunBackfillsOverTenants_FailureIsReportedAndRetried(t *testing.T) {
	f := newFleetFixture(t, 3)
	bf := recordingBackfill{version: 70, fail: true, f: f}

	sum := runBackfillsOverTenants(f.tenants, []tenantbackfills.TenantBackfill{bf}, 0, 1, f.open)
	if len(sum.Failed) != 3 {
		t.Fatalf("sin límite debe intentarlo en los 3 y reportar 3 fallos: %+v", sum.Failed)
	}
	runBackfillsOverTenants(f.tenants, []tenantbackfills.TenantBackfill{bf}, 0, 1, f.open)
	if got := f.ranOn(70); len(got) != 6 {
		t.Fatalf("un intento fallido debe reintentarse en la siguiente corrida: %v", got)
	}
}

func TestRunBackfillsOverTenants_OpenErrorIsReportedAndOthersContinue(t *testing.T) {
	f := newFleetFixture(t, 2)
	tenants := append([]database.Tenant{{Slug: "roto", DBName: "db_inexistente"}}, f.tenants...)
	bf := recordingBackfill{version: 80, f: f}

	sum := runBackfillsOverTenants(tenants, []tenantbackfills.TenantBackfill{bf}, 0, 1, f.open)
	if len(sum.Failed) != 1 || sum.Failed[0].Slug != "roto" {
		t.Fatalf("fallos: %+v", sum.Failed)
	}
	if got := f.ranOn(80); len(got) != 2 {
		t.Fatalf("los demás tenants deben seguir: %v", got)
	}
}
