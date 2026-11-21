package handler

import (
	"fmt"
	"math"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// "Ventas por método de pago" del dashboard debe salir de las líneas de pago reales: una venta
// dividida reparte su total entre los métodos usados (antes caía entera en tenant_sales
// .payment_method), el vuelto se descuenta del efectivo y las anuladas no cuentan.
func TestComputeSalesByPaymentMethod_repartePorLineasDePago(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{&database.TenantSale{}, &database.TenantSalePayment{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	mkSale := func(id uint, total float64, method, status string) {
		if err := db.Create(&database.TenantSale{
			ID: id, BranchID: 1, UserID: 1, SeriesID: 1, DocType: "NOTA DE VENTA", Series: "NV01", Correlative: id,
			Number: fmt.Sprintf("NV01-%d", id), IssueDate: now, Total: total, PaymentMethod: method, Status: status, CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mkPay := func(saleID uint, method string, amount float64) {
		if err := db.Create(&database.TenantSalePayment{SaleID: saleID, Method: method, Amount: amount, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}

	// 1) Dividida: 20 efectivo + 30 yape. payment_method de la venta = "cash" (el primero).
	mkSale(1, 50, "cash", "paid")
	mkPay(1, "cash", 20)
	mkPay(1, "yape", 30)
	// 2) Efectivo con vuelto: paga 100 por 60 → el reporte atribuye 60, no 100.
	mkSale(2, 60, "cash", "paid")
	mkPay(2, "cash", 100)
	// 3) Histórica sin líneas de pago: se atribuye por el método de la venta.
	mkSale(3, 40, "tarjeta", "paid")
	// 4) Anulada: no cuenta.
	mkSale(4, 999, "yape", "cancelled")
	mkPay(4, "yape", 999)
	// 5) Nota de crédito (reverso de un comprobante ya contado): no es una venta nueva. Se guarda
	// como fila de tenant_sales con estado "paid" y total positivo, así que sin excluirla duplicaba
	// el importe (en el desglose, en los totales y en los conteos del dashboard).
	if err := db.Create(&database.TenantSale{
		ID: 5, BranchID: 1, UserID: 1, SeriesID: 1, DocType: "NOTA_CREDITO", Series: "FC01", Correlative: 1,
		Number: "FC01-1", IssueDate: now, Total: 200, PaymentMethod: "cash", Status: "paid", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	from := now.Add(-time.Hour)
	to := now.Add(time.Hour)
	rows, err := computeSalesByPaymentMethod(db, from, to, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]paymentMethodTotal{}
	var sum float64
	for _, r := range rows {
		got[r.Key] = r
		sum += r.Total
	}
	want := map[string]struct {
		total float64
		count int64
	}{"cash": {80, 2}, "yape": {30, 1}, "tarjeta": {40, 1}}
	for k, w := range want {
		g, ok := got[k]
		if !ok || math.Abs(g.Total-w.total) > 0.005 || g.Count != w.count {
			t.Errorf("método %q = %+v, want total %v count %d", k, g, w.total, w.count)
		}
	}
	if len(got) != len(want) {
		t.Errorf("métodos inesperados: %+v", rows)
	}
	// La suma coincide con el total de las ventas vigentes (50 + 60 + 40).
	if math.Abs(sum-150) > 0.005 {
		t.Errorf("suma por método = %v, want 150", sum)
	}
}
