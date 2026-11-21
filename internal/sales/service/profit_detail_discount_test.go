package service

import (
	"fmt"
	"math"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// El reporte de utilidades debe calcular sobre lo REALMENTE cobrado: descuenta el descuento global
// (y de línea) de la venta. Antes usaba unit_price * cantidad, así que una venta con 10% de
// descuento figuraba al precio de lista y no coincidía con "Ventas por producto".
func TestProfitDetail_aplicaDescuentosDeLineaYGlobal(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{&database.TenantSale{}, &database.TenantSaleItem{}, &database.TenantProduct{}, &database.TenantContact{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	mkSale := func(id uint, status string) {
		if err := db.Create(&database.TenantSale{
			ID: id, BranchID: 1, UserID: 1, SeriesID: 1, DocType: "NOTA DE VENTA", Series: "NV01", Correlative: id,
			Number: fmt.Sprintf("NV01-%d", id), IssueDate: now, Status: status, CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	cost := 10.0
	mkItem := func(saleID uint, price, qty, subtotal, lineDisc, globalDisc float64) {
		if err := db.Create(&database.TenantSaleItem{
			SaleID: saleID, Description: "Producto", Quantity: qty, UnitPrice: price, PurchasePrice: &cost,
			Subtotal: subtotal, LineDiscountSubtotal: lineDisc, GlobalDiscountSubtotal: globalDisc, Total: subtotal,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Sin descuento: 50 x 1 (subtotal 50, sin descuentos) → utilidad 40.
	mkSale(1, "paid")
	mkItem(1, 50, 1, 50, 0, 0)
	// Descuento global del 10%: lista 100, subtotal 90 (descuento 10) → precio efectivo 90, utilidad 80.
	mkSale(2, "paid")
	mkItem(2, 100, 1, 90, 0, 10)
	// Descuento de línea + global sobre 2 unidades de 50: lista 100, descuentos 5 + 5, subtotal 90.
	mkSale(3, "paid")
	mkItem(3, 50, 2, 90, 5, 5)
	// Anulada: no cuenta.
	mkSale(4, "cancelled")
	mkItem(4, 500, 1, 500, 0, 0)

	from, to := now.Add(-time.Hour), now.Add(time.Hour)
	rows, summary, err := NewSaleService(db).ProfitDetail(ProfitDetailParams{DateFrom: &from, DateTo: &to})
	if err != nil {
		t.Fatal(err)
	}
	if summary.DistinctSales != 3 || summary.LineItems != 3 {
		t.Fatalf("ventas/líneas = %d/%d, want 3/3 (la anulada no cuenta)", summary.DistinctSales, summary.LineItems)
	}
	// Ventas cobradas: 50 + 90 + 90 = 230; utilidad = 230 - costo (10 + 10 + 20) = 190.
	if math.Abs(summary.TotalSales-230) > 0.01 {
		t.Errorf("TotalSales = %v, want 230", summary.TotalSales)
	}
	if math.Abs(summary.TotalProfit-190) > 0.01 {
		t.Errorf("TotalProfit = %v, want 190", summary.TotalProfit)
	}
	for _, r := range rows {
		if r.SaleID == 2 && math.Abs(r.SalePrice-90) > 0.01 {
			t.Errorf("precio efectivo de la venta 2 = %v, want 90", r.SalePrice)
		}
	}
}
