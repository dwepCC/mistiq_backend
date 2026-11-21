package tenantmigrations

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// V167 agrega sale_unit_id / combo_json / serials_json a las líneas de cotización sin tocar las
// filas existentes, y es idempotente.
func TestV152QuotationItemSaleUnitCombo(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE tenant_quotation_items (id INTEGER PRIMARY KEY AUTOINCREMENT, quotation_id INTEGER, description TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tenant_quotation_items (quotation_id, description) VALUES (1,'previa')`).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := (V167QuotationItemSaleUnitCombo{}).Up(db); err != nil {
			t.Fatalf("Up pasada %d: %v", i+1, err)
		}
	}
	for _, col := range []string{"SaleUnitID", "ComboJSON", "SerialsJSON"} {
		if !db.Migrator().HasColumn(&v167QuotationItem{}, col) {
			t.Errorf("falta la columna %s", col)
		}
	}
	var desc string
	var unit *uint
	db.Raw(`SELECT description, sale_unit_id FROM tenant_quotation_items WHERE id = 1`).Row().Scan(&desc, &unit)
	if desc != "previa" || unit != nil {
		t.Fatalf("la fila previa no debe cambiar: %q %v", desc, unit)
	}
}
