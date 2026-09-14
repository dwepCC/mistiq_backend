package database

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func testHistoryDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TenantMigrationHistory{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// Regresión: Success NO debe llevar un tag `default:...` de GORM. Con ese tag, GORM omite el
// campo del INSERT cuando su valor es el zero-value de Go (false) y deja que la BD aplique su
// propio DEFAULT — así que un `Success: false` explícito (como el que graban recordHistory y
// RecordBackfillHistory ante cualquier migración/backfill fallido) terminaba escribiéndose como
// true, indistinguible de un éxito real. Bug vivo en producción desde 2026-07-20 hasta
// 2026-09-06: todo intento fallido de migración/backfill quedaba registrado como "aplicado".
func TestTenantMigrationHistory_ExplicitFailureIsPersistedAsFailure(t *testing.T) {
	db := testHistoryDB(t)
	cs := "chk"
	if err := db.Create(&TenantMigrationHistory{
		Version: 999, Name: "x", Type: MigrationHistoryTypeBackfill,
		AppliedAt: time.Now(), Success: false, Checksum: &cs,
	}).Error; err != nil {
		t.Fatal(err)
	}

	var row TenantMigrationHistory
	if err := db.Where("version = ? AND type = ?", 999, MigrationHistoryTypeBackfill).
		First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Success {
		t.Fatal("un registro creado con Success: false debe seguir leyéndose como false, no " +
			"reemplazado silenciosamente por un DEFAULT de GORM")
	}
}

func TestTenantMigrationHistory_ExplicitSuccessIsPersistedAsSuccess(t *testing.T) {
	db := testHistoryDB(t)
	cs := "chk"
	if err := db.Create(&TenantMigrationHistory{
		Version: 999, Name: "x", Type: MigrationHistoryTypeBackfill,
		AppliedAt: time.Now(), Success: true, Checksum: &cs,
	}).Error; err != nil {
		t.Fatal(err)
	}

	var row TenantMigrationHistory
	if err := db.Where("version = ? AND type = ?", 999, MigrationHistoryTypeBackfill).
		First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !row.Success {
		t.Fatal("un registro creado con Success: true debe leerse como true")
	}
}
