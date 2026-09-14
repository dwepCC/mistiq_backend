package handler

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupCalcTotalsDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantCashMovement{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// calcSessionTotals alimenta el CurrentBalance de la página server-rendered "Caja"
// (/cashbank/cash) — mismo bug que ya se corrigió en CashBankService.getExpectedBalance: sumaba/
// restaba cualquier movimiento sin mirar el método de pago, así que un egreso por transferencia
// (plata que salió del banco, no del cajón) restaba del efectivo igual que uno real.
func TestCalcSessionTotals_transferExpenseDoesNotReduceCash(t *testing.T) {
	db := setupCalcTotalsDB(t)
	db.Create(&database.TenantCashMovement{CashSessionID: 1, Type: "income", PaymentMethod: "cash", Amount: 500})
	db.Create(&database.TenantCashMovement{CashSessionID: 1, Type: "expense", PaymentMethod: "efectivo", Amount: 100})
	db.Create(&database.TenantCashMovement{CashSessionID: 1, Type: "expense", PaymentMethod: "transferencia", Amount: 700})

	in, out, cur := calcSessionTotals(db, 1, 665.00)
	if in != 500 {
		t.Errorf("totalIn = %v, want 500", in)
	}
	if out != 100 {
		t.Errorf("totalOut = %v, want 100 (el egreso por transferencia no debe contar)", out)
	}
	want := 665.00 + 500 - 100
	if cur != want {
		t.Errorf("current = %v, want %v", cur, want)
	}
	if cur < 0 {
		t.Error("un saldo de caja física nunca debería dar negativo")
	}
}
