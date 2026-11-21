package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func accDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantAccountingSettings{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// Sin fila: se crea con los valores por defecto.
func TestV175CreatesDefaultsWhenNoRow(t *testing.T) {
	db := accDB(t)
	for i := 0; i < 2; i++ {
		if err := (V190AccountingDefaultAccounts{}).Up(db); err != nil {
			t.Fatalf("Up #%d: %v", i+1, err)
		}
	}
	var n int64
	db.Model(&database.TenantAccountingSettings{}).Count(&n)
	if n != 1 {
		t.Fatalf("filas: %d, esperaba 1 (idempotente)", n)
	}
	var st database.TenantAccountingSettings
	db.First(&st)
	if st.ReceivablePEN != "12121" || st.ReceivableUSD != "12122" || st.IgvSalesPEN != "40111" ||
		st.TaxedPEN != "70111" || st.Exportation != "70121" || st.Icbper != "40199" || st.SubdiarioSales != "05" {
		t.Errorf("valores sembrados: %+v", st)
	}
}

// Con fila: se completan solo los vacíos; lo configurado por el tenant no se toca.
func TestV175KeepsCustomAccountsAndFillsEmpty(t *testing.T) {
	db := accDB(t)
	db.Create(&database.TenantAccountingSettings{ReceivablePEN: "0121", TaxedPEN: "7011101"})
	if err := (V190AccountingDefaultAccounts{}).Up(db); err != nil {
		t.Fatal(err)
	}
	var st database.TenantAccountingSettings
	db.First(&st)
	if st.ReceivablePEN != "0121" || st.TaxedPEN != "7011101" {
		t.Errorf("lo configurado debe respetarse: %+v", st)
	}
	if st.IgvSalesPEN != "40111" || st.SubdiarioSales != "05" || st.ReceivableUSD != "12122" {
		t.Errorf("los vacíos deben completarse: %+v", st)
	}
}

func TestV175NoTableIsNoop(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (V190AccountingDefaultAccounts{}).Up(db); err != nil {
		t.Fatalf("sin tabla debe ser no-op: %v", err)
	}
}
