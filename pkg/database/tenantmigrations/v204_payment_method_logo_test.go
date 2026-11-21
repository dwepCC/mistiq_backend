package tenantmigrations

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestV189PaymentMethodLogo_AddsColumnAndIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE tenant_payment_methods (id INTEGER PRIMARY KEY AUTOINCREMENT, code TEXT, name TEXT)`,
		`INSERT INTO tenant_payment_methods (code, name) VALUES ('yape', 'Yape')`,
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := (V204PaymentMethodLogo{}).Up(db); err != nil {
			t.Fatalf("pasada %d: %v", i, err)
		}
	}
	if !db.Migrator().HasColumn("tenant_payment_methods", "logo_url") {
		t.Fatal("falta logo_url")
	}
	var logo *string
	db.Raw("SELECT logo_url FROM tenant_payment_methods WHERE id = 1").Row().Scan(&logo)
	if logo != nil && *logo != "" {
		t.Errorf("el método existente debe quedar sin logo, got %q", *logo)
	}
}
