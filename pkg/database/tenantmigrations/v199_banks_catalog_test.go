package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestV184BanksCatalog_SeedsLinksAndIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantBankAccount{}, &database.TenantPaymentMethod{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantBankAccount{Name: "A", BankName: "  banco de credito del peru ", Active: true})
	db.Create(&database.TenantBankAccount{Name: "B", BankName: "Banco Desconocido", Active: true})
	db.Create(&database.TenantBankAccount{Name: "C", BankName: "BCP", Active: true})
	for i := 0; i < 2; i++ {
		if err := (V199BanksCatalog{}).Up(db); err != nil {
			t.Fatalf("pasada %d: %v", i+1, err)
		}
	}
	var n int64
	db.Model(&database.TenantBank{}).Count(&n)
	if int(n) != len(DefaultBanks) {
		t.Errorf("bancos sembrados = %d, quiere %d (sin duplicar en la 2ª pasada)", n, len(DefaultBanks))
	}
	var a, b database.TenantBankAccount
	db.Where("name = ?", "A").First(&a)
	db.Where("name = ?", "B").First(&b)
	if a.BankID == nil {
		t.Error("la cuenta con banco reconocido debe enlazarse")
	}
	if b.BankID != nil {
		t.Error("la cuenta con banco desconocido queda sin enlazar")
	}
	var c database.TenantBankAccount
	db.Where("name = ?", "C").First(&c)
	if c.BankID == nil {
		t.Error("el alias BCP debe enlazar con Banco de Crédito del Perú")
	}
}
