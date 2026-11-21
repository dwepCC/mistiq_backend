package tenantmigrations

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/dailyfx"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestV182MovementCurrencySnapshot_BackfillsFromDocumentsAndIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantPurchase{}, &database.TenantCashMovement{}, &database.TenantBankMovement{}); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	rate := 3.5
	mkSale := func(id uint, cur string, rt *float64) {
		if err := db.Create(&database.TenantSale{ID: id, BranchID: 1, UserID: 1, DocType: "NV", Series: "NV", Number: fmt.Sprint(id), IssueDate: day, Total: 100, Currency: cur, ExchangeRate: rt, Status: "paid"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mkSale(1, "PEN", nil)
	mkSale(2, "USD", &rate) // con TC
	mkSale(3, "USD", nil)   // sin TC: TC del día
	mkSale(4, "USD", nil)   // sin TC y sin TC del día (otra fecha)
	db.Model(&database.TenantSale{}).Where("id = 4").Update("issue_date", time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	// Movimientos "antiguos": se insertan sin snapshot (SQL directo para saltar el hook de creación).
	ins := func(table string, saleID interface{}, amount float64) {
		if err := db.Exec("INSERT INTO "+table+" (cash_session_id, type, amount, sale_id, user_id, created_at) VALUES (1, 'income', ?, ?, 1, ?)", amount, saleID, day).Error; err != nil {
			t.Fatal(err)
		}
	}
	ins("tenant_cash_movements", nil, 10) // manual
	ins("tenant_cash_movements", 1, 20)   // documento en soles
	ins("tenant_cash_movements", 2, 30)   // USD con TC → 105
	ins("tenant_cash_movements", 3, 40)   // USD sin TC, TC del día 3.6 → 144
	ins("tenant_cash_movements", 4, 50)   // USD sin ningún TC → queda NULL

	prev := dailyfx.Lookup
	dailyfx.Lookup = func(d string) (float64, bool, bool) {
		if d == "2026-10-03" {
			return 3.6, true, true
		}
		return 0, false, false
	}
	t.Cleanup(func() { dailyfx.Lookup = prev })

	for i := 0; i < 2; i++ { // segunda pasada: idempotente
		if err := (V197MovementCurrencySnapshot{}).Up(db); err != nil {
			t.Fatalf("pasada %d: %v", i+1, err)
		}
	}
	var rows []database.TenantCashMovement
	db.Order("id").Find(&rows)
	want := []struct {
		cur string
		pen *float64
	}{{"PEN", f(10)}, {"PEN", f(20)}, {"USD", f(105)}, {"USD", f(144)}, {"USD", nil}}
	for i, w := range want {
		r := rows[i]
		if r.Currency != w.cur || (w.pen == nil) != (r.AmountPEN == nil) || (w.pen != nil && *r.AmountPEN != *w.pen) {
			t.Errorf("fila %d: moneda=%s pen=%v, quiere %s %v", i+1, r.Currency, r.AmountPEN, w.cur, w.pen)
		}
	}
	if rows[2].ExchangeRate == nil || *rows[2].ExchangeRate != 3.5 || rows[3].ExchangeRate == nil || *rows[3].ExchangeRate != 3.6 {
		t.Errorf("TC guardados: %v %v", rows[2].ExchangeRate, rows[3].ExchangeRate)
	}
	// El importe original no se toca.
	if rows[2].Amount != 30 {
		t.Errorf("amount original = %v", rows[2].Amount)
	}
}

func f(v float64) *float64 { return &v }
