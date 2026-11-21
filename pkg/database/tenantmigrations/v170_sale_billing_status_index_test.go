package tenantmigrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestV155SaleBillingStatusIndex(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:v170_idx?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE tenant_sales (id INTEGER PRIMARY KEY AUTOINCREMENT, billing_status TEXT, total REAL)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tenant_sales (billing_status, total) VALUES ('pending', 10)`).Error; err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ { // idempotente
		if err := (V170SaleBillingStatusIndex{}).Up(db); err != nil {
			t.Fatalf("Up pasada %d: %v", i+1, err)
		}
	}
	if !db.Migrator().HasIndex(&v170Sale{}, idxSaleBillingStatus) {
		t.Fatal("falta el índice")
	}
	var n int64
	db.Raw(`SELECT COUNT(*) FROM tenant_sales WHERE billing_status='pending' AND total=10`).Scan(&n)
	if n != 1 {
		t.Fatal("los datos deben quedar intactos")
	}

	// Tenant sin tabla de ventas: no falla.
	empty, _ := gorm.Open(sqlite.Open("file:v170_empty?mode=memory&cache=shared"), &gorm.Config{})
	if err := (V170SaleBillingStatusIndex{}).Up(empty); err != nil {
		t.Fatalf("sin tabla no debe fallar: %v", err)
	}
}
