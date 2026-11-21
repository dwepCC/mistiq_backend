package handler

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// El dashboard suma las ventas en SOLES: una venta en USD aporta total × TC de la venta (antes sumaba los dólares como soles).
func TestDashboardSales_SumUSDSalesInSoles(t *testing.T) {
	db := setupTopProductsDB(t)
	now := time.Now()
	rate := 3.5
	mk := func(num, cur string, rt *float64, total float64, status string) {
		if err := db.Create(&database.TenantSale{BranchID: 1, UserID: 1, DocType: "NOTA_VENTA", Series: "NV01", Number: num,
			IssueDate: now, Total: total, Subtotal: total, Currency: cur, ExchangeRate: rt, Status: status}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk("1", "PEN", nil, 100, "paid")
	mk("2", "USD", &rate, 100, "paid")      // S/ 350
	mk("3", "USD", &rate, 999, "cancelled") // anulada: no suma
	var got float64
	analyticsSaleScope(db, now.Add(-24*time.Hour), now.Add(24*time.Hour), 0, 0, false).Select(sumSalesPENSelect()).Scan(&got)
	if got != 450 { // 100 + 100 × 3.5 (no 200 mezclando dólares como soles)
		t.Errorf("ventas en soles = %v, quiere 450", got)
	}
}

// El recargo al consumo cobrado dentro del total no es ingreso del negocio: el dashboard lo resta (en soles y en dólares).
func TestDashboardSales_RestaElRecargoAlConsumo(t *testing.T) {
	db := setupTopProductsDB(t)
	now := time.Now()
	rate := 3.5
	for _, s := range []database.TenantSale{
		{Number: "1", Currency: "PEN", Total: 128, ServiceChargeAmount: 10, ServiceChargeRate: 10}, // 118 de ventas
		{Number: "2", Currency: "USD", ExchangeRate: &rate, Total: 100, ServiceChargeAmount: 10},   // 90 × 3.5 = 315
		{Number: "3", Currency: "PEN", Total: 50},                                                  // sin recargo
	} {
		s.BranchID, s.UserID, s.DocType, s.Series, s.IssueDate, s.Subtotal, s.Status = 1, 1, "NOTA_VENTA", "NV01", now, s.Total, "paid"
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
	}
	var got float64
	analyticsSaleScope(db, now.Add(-24*time.Hour), now.Add(24*time.Hour), 0, 0, false).Select(sumSalesPENSelect()).Scan(&got)
	if got != 118+315+50 {
		t.Errorf("ventas netas del recargo = %v, quiere 483", got)
	}
}
