package tenantmigrations

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func v099SetupDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE tenant_products (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			code TEXT NOT NULL,
			sale_price REAL NOT NULL,
			is_restaurant INTEGER DEFAULT 0
		)`,
		`CREATE TABLE tenant_preparation_areas (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			slug TEXT NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE tenant_comandas (
			id INTEGER PRIMARY KEY,
			product_name TEXT NOT NULL,
			preparation_area TEXT,
			quantity REAL NOT NULL,
			unit_price REAL NOT NULL
		)`,
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestV099Combos_Idempotent(t *testing.T) {
	db := v099SetupDB(t)
	mig := V099Combos{}

	if err := mig.Up(db); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if !db.Migrator().HasColumn(&v099Product{}, "HasCombo") {
		t.Fatal("esperaba tenant_products.has_combo")
	}
	for _, tbl := range []any{&v099ComboGroup{}, &v099ComboGroupItem{}} {
		if !db.Migrator().HasTable(tbl) {
			t.Fatalf("esperaba tabla %T", tbl)
		}
	}
	for _, col := range []string{"PreparationAreaID", "ComboParentKey", "ComboJSON"} {
		if !db.Migrator().HasColumn(&v099Comanda{}, col) {
			t.Fatalf("esperaba tenant_comandas.%s", col)
		}
	}

	if err := mig.Up(db); err != nil {
		t.Fatalf("second Up (idempotente): %v", err)
	}
}

func TestV099Combos_BackfillComandaPreparationAreaID(t *testing.T) {
	db := v099SetupDB(t)

	if err := db.Exec(`INSERT INTO tenant_preparation_areas (id, name, slug, deleted_at) VALUES
		(7, 'Cocina', 'cocina', NULL),
		(9, 'Bar', 'bar', NULL),
		(11, 'Postres', 'postres', '2026-01-01 00:00:00')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tenant_comandas (id, product_name, preparation_area, quantity, unit_price) VALUES
		(1, 'Pollo a la brasa', 'cocina', 1, 20),
		(2, 'Agua mineral', 'bar', 1, 2.5),
		(3, 'Manual sin area', '', 1, 5),
		(4, 'Area inexistente', 'parrilla', 1, 8),
		(5, 'Area borrada', 'postres', 1, 9)`).Error; err != nil {
		t.Fatal(err)
	}

	if err := (V099Combos{}).Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	type row struct {
		ID                uint
		PreparationAreaID *uint
	}
	var got []row
	if err := db.Raw(`SELECT id, preparation_area_id FROM tenant_comandas ORDER BY id`).Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("esperaba 5 comandas, got %d", len(got))
	}

	want := map[uint]*uint{
		1: ptrUint(7), // cocina → id 7
		2: ptrUint(9), // bar → id 9
		3: nil,        // sin slug: queda nulo
		4: nil,        // slug sin área: queda nulo
		5: nil,        // área soft-deleted: no se vincula
	}
	for _, r := range got {
		exp := want[r.ID]
		switch {
		case exp == nil && r.PreparationAreaID != nil:
			t.Errorf("comanda %d: esperaba nulo, got %d", r.ID, *r.PreparationAreaID)
		case exp != nil && r.PreparationAreaID == nil:
			t.Errorf("comanda %d: esperaba %d, got nulo", r.ID, *exp)
		case exp != nil && *exp != *r.PreparationAreaID:
			t.Errorf("comanda %d: esperaba %d, got %d", r.ID, *exp, *r.PreparationAreaID)
		}
	}
}

func ptrUint(v uint) *uint { return &v }
