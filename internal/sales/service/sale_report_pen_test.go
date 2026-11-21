package service

import (
	"testing"
	"time"

	"tukifac/pkg/dailyfx"
	"tukifac/pkg/database"
)

// Reporte de ventas: valor original por moneda + equivalente en soles por fila y consolidado.
func TestSaleList_PENEquivalentRowsAndSummary(t *testing.T) {
	db := setupPriceHardeningDB(t)
	if err := db.AutoMigrate(&database.TenantSaleDetraccion{}); err != nil {
		t.Skipf("tabla de detracción no disponible en el esquema de prueba: %v", err)
	}
	day := func(d int) time.Time { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC) }
	rate := 3.5
	mk := func(num string, cur string, rt *float64, total float64, status string, issue time.Time) {
		if err := db.Create(&database.TenantSale{BranchID: 1, UserID: 1, DocType: "NOTA_VENTA", Series: "NV01", Number: num,
			IssueDate: issue, Total: total, Subtotal: total, Currency: cur, ExchangeRate: rt, Status: status}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk("1", "PEN", nil, 100, "paid", day(1))
	mk("2", "USD", &rate, 100, "paid", day(2))     // 350 con su TC
	mk("3", "USD", nil, 50, "paid", day(3))        // sin TC: TC del día 3.6 → 180
	mk("4", "USD", nil, 10, "paid", day(9))        // sin TC y sin TC del día → no convertible
	mk("5", "USD", &rate, 999, "cancelled", day(4)) // anulada: no entra al consolidado

	prev := dailyfx.Lookup
	dailyfx.Lookup = func(d string) (float64, bool, bool) {
		if d == "2026-10-03" {
			return 3.6, true, true
		}
		return 0, false, false
	}
	t.Cleanup(func() { dailyfx.Lookup = prev })

	sales, _, sum, err := NewSaleService(db).List(SaleListParams{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	byNum := map[string]database.TenantSale{}
	for _, s := range sales {
		byNum[s.Number] = s
	}
	pen := func(n string) float64 {
		if v := byNum[n].TotalPEN; v != nil {
			return *v
		}
		return -1
	}
	if pen("1") != 100 || pen("2") != 350 || pen("3") != 180 || pen("4") != -1 {
		t.Errorf("equivalentes por fila: %v %v %v %v", pen("1"), pen("2"), pen("3"), pen("4"))
	}
	if !byNum["3"].RateEstimated || byNum["2"].RateEstimated || byNum["1"].RateEffective != nil {
		t.Errorf("TC estimado solo donde faltaba: %+v", byNum["3"].RateEstimated)
	}
	// Consolidado: PEN 100 + USD (350 + 180) = 630; 1 estimada, 1 sin TC.
	if sum.SumActivePEN != 630 || sum.RateEstimatedCount != 1 || sum.RateMissingCount != 1 {
		t.Errorf("resumen: pen=%v estimadas=%d sin_tc=%d", sum.SumActivePEN, sum.RateEstimatedCount, sum.RateMissingCount)
	}
	cur := map[string]SaleListCurrencySummary{}
	for _, c := range sum.ByCurrency {
		cur[c.Currency] = c
	}
	if cur["PEN"].SumActivePEN != 100 || cur["USD"].SumActivePEN != 530 || cur["USD"].SumActive != 160 {
		t.Errorf("por moneda: PEN=%+v USD=%+v", cur["PEN"], cur["USD"])
	}
}
