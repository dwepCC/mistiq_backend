package engine

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func testTenantDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantMigrationHistory{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func insertHistory(t *testing.T, db *gorm.DB, version int, name string, success bool) {
	t.Helper()
	cs := "chk"
	if err := db.Create(&database.TenantMigrationHistory{
		Version: version, Name: name, Type: database.MigrationHistoryTypeSchema,
		AppliedAt: time.Now(), Success: success, Checksum: &cs,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestDeriveCurrentVersionFromHistory_empty(t *testing.T) {
	db := testTenantDB(t)
	v, err := DeriveCurrentVersionFromHistory(db)
	if err != nil || v != 0 {
		t.Fatalf("expected 0, got %d err=%v", v, err)
	}
}

func TestDeriveCurrentVersionFromHistory_chain(t *testing.T) {
	db := testTenantDB(t)
	insertHistory(t, db, 1, "baseline", true)
	insertHistory(t, db, 31, "multi_branch", true)
	insertHistory(t, db, 32, "restaurant_orders", true)

	v, err := DeriveCurrentVersionFromHistory(db)
	if err != nil || v != 32 {
		t.Fatalf("expected 32, got %d err=%v", v, err)
	}
}

func TestDeriveCurrentVersionFromHistory_gapStopsChain(t *testing.T) {
	db := testTenantDB(t)
	insertHistory(t, db, 1, "baseline", true)
	insertHistory(t, db, 33, "delivery", true) // V031 missing

	v, err := DeriveCurrentVersionFromHistory(db)
	if err != nil || v != 1 {
		t.Fatalf("expected 1, got %d err=%v", v, err)
	}
}

func TestInvalidateSchemaHistoryFromVersion(t *testing.T) {
	db := testTenantDB(t)
	insertHistory(t, db, 1, "baseline", true)
	insertHistory(t, db, 58, "notes", true)
	insertHistory(t, db, 59, "user_branches", true)
	insertHistory(t, db, 60, "receipt", true)

	n, err := invalidateSchemaHistoryFromVersion(db, 59)
	if err != nil || n != 2 {
		t.Fatalf("expected 2 rows invalidated, got %d err=%v", n, err)
	}
	applied59, _ := isHistoryApplied(db, 59, database.MigrationHistoryTypeSchema)
	applied58, _ := isHistoryApplied(db, 58, database.MigrationHistoryTypeSchema)
	if applied59 || !applied58 {
		t.Fatalf("V059 debe quedar invalidada y V058 intacta (59=%v 58=%v)", applied59, applied58)
	}
}
