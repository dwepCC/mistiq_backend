package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"tukifac/pkg/database"
)

func TestDashboardDayKey(t *testing.T) {
	cases := map[string]string{
		"2026-05-26":           "2026-05-26",
		"2026-05-26T00:00:00Z": "2026-05-26",
		" 2026-05-26 ":         "2026-05-26",
	}
	for in, want := range cases {
		if got := dashboardDayKey(in); got != want {
			t.Fatalf("dashboardDayKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// El gráfico "ventas por método de pago" no debe sumar el vuelto: tenant_sale_payments guarda lo
// entregado por el cliente, y el excedente se descuenta únicamente del efectivo.
func TestLoadSalesByPayment_discountsChangeFromCash(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSalePayment{}); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	mk := func(total float64, pays ...database.TenantSalePayment) {
		sale := database.TenantSale{BranchID: 1, DocType: "03", IssueDate: day, Total: total, Status: "paid"}
		if err := db.Create(&sale).Error; err != nil {
			t.Fatal(err)
		}
		for _, p := range pays {
			p.SaleID = sale.ID
			if err := db.Create(&p).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	// A: total 20, paga 50 en efectivo (vuelto 30). B: total 40, efectivo 10 + yape 30. C: total 15 con yape 15.
	mk(20, database.TenantSalePayment{Method: "cash", Amount: 50})
	mk(40, database.TenantSalePayment{Method: "cash", Amount: 10}, database.TenantSalePayment{Method: "yape", Amount: 30})
	mk(15, database.TenantSalePayment{Method: "yape", Amount: 15})

	out := &DashboardData{}
	err = NewDashboardService(db).loadSalesByPayment(1, day.Add(-24*time.Hour), day.Add(24*time.Hour), DashboardFilter{}, out)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DashboardPaymentRow{}
	var sum float64
	for _, r := range out.SalesByPaymentMethod {
		got[r.Method] = r
		sum += r.Total
	}
	if got["efectivo"].Total != 30 || got["efectivo"].Count != 2 {
		t.Fatalf("efectivo = %+v, want total 30 (20 + 10, sin los 30 de vuelto) en 2 ventas", got["efectivo"])
	}
	if got["yape"].Total != 45 || got["yape"].Count != 2 {
		t.Fatalf("yape = %+v, want total 45 en 2 ventas", got["yape"])
	}
	if sum != 75 {
		t.Fatalf("suma de métodos = %v, want 75 (= suma de los totales de venta 20+40+15)", sum)
	}
}
