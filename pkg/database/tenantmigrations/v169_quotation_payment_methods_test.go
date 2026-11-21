package tenantmigrations

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// V169 agrega la columna a una tabla previa sin tocar las cotizaciones existentes y es idempotente.
func TestV154QuotationPaymentMethods(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE tenant_quotations (id INTEGER PRIMARY KEY AUTOINCREMENT, number TEXT, total REAL)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tenant_quotations (number, total) VALUES ('C-1', 10)`).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := (V169QuotationPaymentMethods{}).Up(db); err != nil {
			t.Fatalf("Up pasada %d: %v", i+1, err)
		}
	}
	if !db.Migrator().HasColumn(&v169Quotation{}, "PaymentMethodsJSON") {
		t.Fatal("falta la columna payment_methods_json")
	}
	var n int64
	db.Raw(`SELECT COUNT(*) FROM tenant_quotations WHERE number='C-1' AND total=10`).Scan(&n)
	if n != 1 {
		t.Fatal("la cotización previa debe quedar intacta")
	}
	// Sin la tabla (tenant sin cotizaciones) no falla.
	db2, _ := gorm.Open(sqlite.Open("file:v169_empty?mode=memory&cache=shared"), &gorm.Config{})
	if err := (V169QuotationPaymentMethods{}).Up(db2); err != nil {
		t.Fatalf("sin tabla no debe fallar: %v", err)
	}
}
