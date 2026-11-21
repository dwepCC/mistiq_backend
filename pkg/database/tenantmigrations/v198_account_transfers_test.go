package tenantmigrations

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestV183AccountTransfers_BackfillsAccountAmountAndIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantBankAccount{}, &database.TenantBankMovement{}); err != nil {
		t.Fatal(err)
	}
	pen := database.TenantBankAccount{Name: "Soles", Currency: "PEN", Active: true}
	usd := database.TenantBankAccount{Name: "Dólares", Currency: "USD", Active: true}
	db.Create(&pen)
	db.Create(&usd)
	now := time.Now()
	// Filas antiguas (sin account_amount): SQL directo para saltar el hook.
	ins := func(acc uint, amount float64, cur string, amountPEN interface{}) {
		if err := db.Exec(`INSERT INTO tenant_bank_movements (bank_account_id, type, amount, date, user_id, currency, amount_pen, created_at)
			VALUES (?, 'credit', ?, ?, 1, ?, ?, ?)`, acc, amount, now, cur, amountPEN, now).Error; err != nil {
			t.Fatal(err)
		}
	}
	ins(pen.ID, 50, "PEN", 50)     // soles → 50
	ins(pen.ID, 100, "USD", 380)   // documento USD en cuenta en soles → 380 (equivalente)
	ins(pen.ID, 70, "USD", nil)    // USD sin TC → queda sin valuar
	ins(usd.ID, 100, "USD", 380)   // cuenta en dólares, movimiento en dólares → 100

	for i := 0; i < 2; i++ {
		if err := (V198AccountTransfers{}).Up(db); err != nil {
			t.Fatalf("pasada %d: %v", i+1, err)
		}
	}
	var rows []database.TenantBankMovement
	db.Order("id").Find(&rows)
	want := []*float64{f(50), f(380), nil, f(100)}
	for i, w := range want {
		got := rows[i].AccountAmount
		if (w == nil) != (got == nil) || (w != nil && *got != *w) {
			t.Errorf("fila %d: account_amount=%v, quiere %v", i+1, got, w)
		}
	}
	if !db.Migrator().HasTable(&database.TenantAccountTransfer{}) {
		t.Error("debe crear tenant_account_transfers")
	}
}
