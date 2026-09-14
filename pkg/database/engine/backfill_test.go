package engine

import (
	"testing"

	"gorm.io/gorm"

	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantbackfills"
)

// fakeRunOnceBackfill simula un backfill normal (no repeatable) para probar shouldSkipBackfill
// sin depender de un TenantBackfill real registrado.
type fakeRunOnceBackfill struct{ version int }

func (f fakeRunOnceBackfill) Version() int              { return f.version }
func (fakeRunOnceBackfill) Name() string                { return "fake_run_once" }
func (fakeRunOnceBackfill) Run(db *gorm.DB) error        { return nil }

// fakeRepeatableBackfill simula uno declarado tenantbackfills.Repeatable.
type fakeRepeatableBackfill struct{ version int }

func (f fakeRepeatableBackfill) Version() int       { return f.version }
func (fakeRepeatableBackfill) Name() string         { return "fake_repeatable" }
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

	skip, err := shouldSkipBackfill(db, 999, fakeRunOnceBackfill{version: 999})
	if err != nil {
		t.Fatal(err)
	}
	if !skip {
		t.Fatal("un backfill run-once ya aplicado con éxito debe saltarse")
	}
}

func TestShouldSkipBackfill_RunOnce_RunsWhenNotYetApplied(t *testing.T) {
	db := testTenantDB(t)

	skip, err := shouldSkipBackfill(db, 999, fakeRunOnceBackfill{version: 999})
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

	skip, err := shouldSkipBackfill(db, 999, fakeRunOnceBackfill{version: 999})
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("un intento previo fallido (success=false) no debe bloquear el reintento")
	}
}

func TestShouldSkipBackfill_Repeatable_NeverSkipsEvenIfAlreadyApplied(t *testing.T) {
	db := testTenantDB(t)
	insertBackfillHistory(t, db, 999, true)

	skip, err := shouldSkipBackfill(db, 999, fakeRepeatableBackfill{version: 999})
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("un backfill Repeatable nunca debe saltarse por historial previo, aunque haya " +
			"un éxito registrado — ese es justo el bug que Repeatable existe para evitar: un " +
			"schema dependiente que aún estaba a medio desplegar cuando el backfill 'tuvo éxito' " +
			"resolviendo poco o nada, dejándolo trabado para siempre")
	}
}

func TestShouldSkipBackfill_RealV036IsWiredAsRepeatable(t *testing.T) {
	db := testTenantDB(t)
	bf := tenantbackfills.V036SalePaymentCashSessionBackfill{}
	insertBackfillHistory(t, db, bf.Version(), true)

	skip, err := shouldSkipBackfill(db, bf.Version(), bf)
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("V036SalePaymentCashSessionBackfill debe seguir siendo elegible para re-correr " +
			"incluso con un éxito previo registrado en tenant_migration_history")
	}
}
