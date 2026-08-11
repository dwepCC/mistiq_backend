package tenantmigrations

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestV079ProductPreparationArea_Idempotent(t *testing.T) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE tenant_products (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			code TEXT NOT NULL,
			sale_price REAL NOT NULL,
			is_restaurant INTEGER DEFAULT 0
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	mig := V079ProductPreparationArea{}
	if err := mig.Up(db); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if !db.Migrator().HasColumn(&v079Product{}, "PreparationArea") {
		t.Fatal("expected preparation_area after first Up")
	}
	if err := mig.Up(db); err != nil {
		t.Fatalf("second Up (idempotent): %v", err)
	}
}
