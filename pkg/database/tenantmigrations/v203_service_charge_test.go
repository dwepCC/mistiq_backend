package tenantmigrations

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Sobre un esquema ANTERIOR (sin las columnas) agrega solo lo suyo, deja las filas existentes idénticas (apagado / 0) y es idempotente.
func TestV188ServiceCharge_AddsColumnsWithoutTouchingRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE tenant_company_configs (id INTEGER PRIMARY KEY AUTOINCREMENT, business_name TEXT)`,
		`CREATE TABLE tenant_sales (id INTEGER PRIMARY KEY AUTOINCREMENT, total REAL)`,
		`INSERT INTO tenant_company_configs (business_name) VALUES ('ACME')`,
		`INSERT INTO tenant_sales (total) VALUES (118)`,
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := (V203ServiceCharge{}).Up(db); err != nil {
			t.Fatalf("pasada %d: %v", i, err)
		}
	}
	mig := db.Migrator()
	for table, cols := range map[string][]string{
		"tenant_company_configs": {"service_charge_enabled", "service_charge_rate"},
		"tenant_sales":           {"service_charge_rate", "service_charge_amount"},
	} {
		for _, c := range cols {
			if !mig.HasColumn(table, c) {
				t.Errorf("falta %s.%s", table, c)
			}
		}
	}
	var en bool
	var rate float64
	db.Raw("SELECT service_charge_enabled, service_charge_rate FROM tenant_company_configs WHERE id = 1").Row().Scan(&en, &rate)
	if en || rate != 10 {
		t.Errorf("config existente debe quedar apagada con 10%% propuesto: enabled=%v rate=%v", en, rate)
	}
	var r, a float64
	db.Raw("SELECT service_charge_rate, service_charge_amount FROM tenant_sales WHERE id = 1").Row().Scan(&r, &a)
	if r != 0 || a != 0 {
		t.Errorf("venta existente debe quedar sin recargo: %v %v", r, a)
	}
}
