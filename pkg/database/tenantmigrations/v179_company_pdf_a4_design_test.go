package tenantmigrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// La v179 solo agrega una columna con default: la fila existente queda en 'modern' y la migración
// es idempotente.
func TestV164_AgregaDisenoConDefaultSinTocarFilas(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_pragma=foreign_keys(0)"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE tenant_company_configs (id integer primary key, business_name text)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tenant_company_configs (id, business_name) VALUES (1, 'ACME')`).Error; err != nil {
		t.Fatal(err)
	}
	mig := V179CompanyPdfA4Design{}
	for i := 0; i < 2; i++ {
		if err := mig.Up(db); err != nil {
			t.Fatalf("Up #%d: %v", i+1, err)
		}
	}
	if !db.Migrator().HasColumn("tenant_company_configs", "pdf_a4_design") {
		t.Fatal("falta la columna pdf_a4_design")
	}
	var design, name string
	if err := db.Raw(`SELECT pdf_a4_design, business_name FROM tenant_company_configs WHERE id = 1`).Row().Scan(&design, &name); err != nil {
		t.Fatal(err)
	}
	if design != "modern" || name != "ACME" {
		t.Errorf("fila alterada: design=%q name=%q", design, name)
	}
	if mig.Version() != 179 {
		t.Errorf("version = %d", mig.Version())
	}
}

// Sin la tabla (tenant recién creado antes de AutoMigrate) no falla.
func TestV164_SinTablaNoFalla(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:v179empty?mode=memory&cache=shared&_pragma=foreign_keys(0)"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (V179CompanyPdfA4Design{}).Up(db); err != nil {
		t.Fatal(err)
	}
}
