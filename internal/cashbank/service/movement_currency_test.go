package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/dailyfx"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func ptrU(v uint) *uint { return &v }

// Reporte de caja: cada ingreso muestra su moneda original y su equivalente en soles; los totales se separan por moneda y
// se consolidan en soles; el saldo físico se calcula en soles.
func TestMovementReport_CurrencyEquivalentsAndPhysicalBalanceInSoles(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantPurchase{}); err != nil {
		t.Fatal(err)
	}
	issue := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	rate := 3.5
	pen := database.TenantSale{BranchID: 1, UserID: 1, DocType: "NOTA_VENTA", Series: "NV", Number: "1", IssueDate: issue, Total: 100, Currency: "PEN", Status: "paid"}
	usd := database.TenantSale{BranchID: 1, UserID: 1, DocType: "NOTA_VENTA", Series: "NV", Number: "2", IssueDate: issue, Total: 100, Currency: "USD", ExchangeRate: &rate, Status: "paid"}
	usdNoRate := database.TenantSale{BranchID: 1, UserID: 1, DocType: "NOTA_VENTA", Series: "NV", Number: "3", IssueDate: issue, Total: 50, Currency: "USD", Status: "paid"}
	usdNoAny := database.TenantSale{BranchID: 1, UserID: 1, DocType: "NOTA_VENTA", Series: "NV", Number: "4", IssueDate: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC), Total: 20, Currency: "USD", Status: "paid"}
	for _, s := range []*database.TenantSale{&pen, &usd, &usdNoRate, &usdNoAny} {
		if err := db.Create(s).Error; err != nil {
			t.Fatal(err)
		}
	}
	prev := dailyfx.Lookup
	dailyfx.Lookup = func(day string) (float64, bool, bool) {
		if day == "2026-10-03" {
			return 3.6, true, true
		}
		return 0, false, false
	}
	t.Cleanup(func() { dailyfx.Lookup = prev })

	rows := []MovementReportRow{
		{Type: "venta", Amount: 100, SaleID: ptrU(pen.ID)},
		{Type: "venta", Amount: 100, SaleID: ptrU(usd.ID)},      // US$ 100 al 3.5 = S/ 350
		{Type: "venta", Amount: 50, SaleID: ptrU(usdNoRate.ID)}, // sin TC: TC del día 3.6 = S/ 180
		{Type: "venta", Amount: 20, SaleID: ptrU(usdNoAny.ID)},  // sin TC ni TC del día: fuera del total en soles
		{Type: "egreso", Amount: -30},                           // manual: soles
	}
	NewCashBankService(db).enrichMovementRowsCurrency(rows)

	if rows[0].Currency != "PEN" || rows[0].AmountPEN != 100 {
		t.Errorf("PEN: %+v", rows[0])
	}
	if rows[1].Currency != "USD" || rows[1].AmountPEN != 350 || rows[1].RateEstimated {
		t.Errorf("USD con TC: %+v", rows[1])
	}
	if rows[2].AmountPEN != 180 || !rows[2].RateEstimated {
		t.Errorf("USD sin TC guardado debe usar el del día y marcarse estimado: %+v", rows[2])
	}
	if !rows[3].RateMissing || rows[3].AmountPEN != 0 {
		t.Errorf("USD sin ningún TC: %+v", rows[3])
	}
	if rows[4].Currency != "PEN" || rows[4].AmountPEN != -30 {
		t.Errorf("manual en soles: %+v", rows[4])
	}

	opening := 1000.0
	sum := buildMovementChannelBlock(rows, 0, 0, &opening).Summary
	// Soles: 100 ingresos, 30 egresos. USD: 170 ingresos (100+50+20) → en soles 350+180 = 530.
	if sum.SumIncomePEN != 630 || sum.SumExpensePEN != 30 || sum.NetMovementPEN != 600 {
		t.Errorf("consolidado en soles: ing=%v egr=%v neto=%v", sum.SumIncomePEN, sum.SumExpensePEN, sum.NetMovementPEN)
	}
	if sum.PhysicalBalance == nil || *sum.PhysicalBalance != 1600 { // 1000 + 600 (no 1000 + 140 mezclando dólares)
		t.Errorf("saldo físico en soles = %v, quiere 1600", sum.PhysicalBalance)
	}
	if sum.RateEstimatedCount != 1 || sum.RateMissingCount != 1 {
		t.Errorf("estimadas=%d sin_tc=%d", sum.RateEstimatedCount, sum.RateMissingCount)
	}
	by := map[string]MovementCurrencyTotal{}
	for _, c := range sum.ByCurrency {
		by[c.Currency] = c
	}
	if by["PEN"].SumIncome != 100 || by["PEN"].SumExpense != 30 || by["USD"].SumIncome != 170 || by["USD"].SumIncomePEN != 530 {
		t.Errorf("por moneda: PEN=%+v USD=%+v", by["PEN"], by["USD"])
	}
	if sum.ByCurrency[0].Currency != "PEN" {
		t.Errorf("PEN va primero: %+v", sum.ByCurrency)
	}
}
