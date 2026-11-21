package tenantmigrations

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// V168 agrega la columna con default true: los tenants existentes conservan el desglose de IGV en
// sus notas de venta; es idempotente y permite apagarlo despues.
func TestV153CompanyShowIgvBreakdownOnSaleNote(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE tenant_company_configs (id INTEGER PRIMARY KEY AUTOINCREMENT, business_name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tenant_company_configs (business_name) VALUES ('ACME')`).Error; err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := (V168CompanyShowIgvBreakdownOnSaleNote{}).Up(db); err != nil {
			t.Fatalf("Up pasada %d: %v", i+1, err)
		}
	}
	if !db.Migrator().HasColumn(&v168CompanyConfig{}, "ShowIgvBreakdownOnSaleNote") {
		t.Fatal("falta la columna show_igv_breakdown_on_sale_note")
	}

	var v bool
	if err := db.Raw(`SELECT show_igv_breakdown_on_sale_note FROM tenant_company_configs LIMIT 1`).Scan(&v).Error; err != nil {
		t.Fatal(err)
	}
	if !v {
		t.Fatal("la fila previa debe quedar con el desglose activo (default true)")
	}

	if err := db.Exec(`UPDATE tenant_company_configs SET show_igv_breakdown_on_sale_note = 0`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT show_igv_breakdown_on_sale_note FROM tenant_company_configs LIMIT 1`).Scan(&v).Error; err != nil {
		t.Fatal(err)
	}
	if v {
		t.Fatal("debe poder desactivarse")
	}
}
