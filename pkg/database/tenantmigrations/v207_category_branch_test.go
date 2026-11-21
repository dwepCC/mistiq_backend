package tenantmigrations

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Solo agrega la columna (nullable): las categorías existentes quedan en NULL = todas las sucursales, sin backfill. Idempotente.
func TestV192CategoryBranch_AddsNullableColumnWithoutTouchingRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE tenant_categories (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)`,
		`INSERT INTO tenant_categories (name) VALUES ('Bebidas'), ('Snacks')`,
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := (V207CategoryBranch{}).Up(db); err != nil {
			t.Fatalf("pasada %d: %v", i, err)
		}
	}
	if !db.Migrator().HasColumn("tenant_categories", "branch_id") {
		t.Fatal("falta tenant_categories.branch_id")
	}
	var nulls int64
	db.Raw("SELECT COUNT(*) FROM tenant_categories WHERE branch_id IS NULL").Scan(&nulls)
	if nulls != 2 {
		t.Fatalf("las 2 categorías existentes debían quedar en NULL (todas las sucursales), hay %d", nulls)
	}
}
