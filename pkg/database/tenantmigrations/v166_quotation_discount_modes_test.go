package tenantmigrations

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// V166 agrega las columnas de descuento y corrige SOLO las filas cuyos importes prueban que el
// precio no incluía IGV; es idempotente.
func TestV151QuotationDiscountModes(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE tenant_quotations (id INTEGER PRIMARY KEY AUTOINCREMENT, total REAL)`,
		`CREATE TABLE tenant_quotation_items (id INTEGER PRIMARY KEY AUTOINCREMENT, quantity REAL, unit_price REAL, discount REAL,
			igv_affectation_type TEXT, price_includes_igv INTEGER, subtotal REAL, tax_amount REAL, total REAL)`,
		// (1) 2 x 50 SIN IGV, descuento 5: subtotal 95, total 112.10 -> estaba mal guardado como incluido.
		`INSERT INTO tenant_quotation_items (quantity,unit_price,discount,igv_affectation_type,price_includes_igv,subtotal,tax_amount,total) VALUES (2,50,5,'10',1,95,17.10,112.10)`,
		// (2) 1 x 20 CON IGV: total 20, subtotal 16.95 -> correcto, no se toca.
		`INSERT INTO tenant_quotation_items (quantity,unit_price,discount,igv_affectation_type,price_includes_igv,subtotal,tax_amount,total) VALUES (1,20,0,'10',1,16.95,3.05,20)`,
		// (3) exonerado: subtotal = total, sin IGV -> ambiguo, no se toca.
		`INSERT INTO tenant_quotation_items (quantity,unit_price,discount,igv_affectation_type,price_includes_igv,subtotal,tax_amount,total) VALUES (1,100,0,'20',1,100,0,100)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < 2; i++ {
		if err := (V166QuotationDiscountModes{}).Up(db); err != nil {
			t.Fatalf("Up pasada %d: %v", i+1, err)
		}
	}
	for _, col := range []string{"GlobalDiscountMode", "GlobalDiscountValue", "GlobalDiscountAmount"} {
		if !db.Migrator().HasColumn(&v166Quotation{}, col) {
			t.Errorf("falta tenant_quotations.%s", col)
		}
	}
	for _, col := range []string{"LineDiscountMode", "LineDiscountValue"} {
		if !db.Migrator().HasColumn(&v166QuotationItem{}, col) {
			t.Errorf("falta tenant_quotation_items.%s", col)
		}
	}
	flag := func(id int) int {
		var v int
		db.Raw(`SELECT price_includes_igv FROM tenant_quotation_items WHERE id = ?`, id).Scan(&v)
		return v
	}
	if flag(1) != 0 {
		t.Error("la fila 1 (precio sin IGV mal guardado) debía corregirse a 0")
	}
	if flag(2) != 1 || flag(3) != 1 {
		t.Error("las filas correctas o ambiguas no deben tocarse")
	}
}
