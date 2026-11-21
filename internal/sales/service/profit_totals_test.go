package service

import (
	"math"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func TestProfitDetailSummaryHasCostAndFiltersByUserAndProduct(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:profit_totals?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE tenant_products (id INTEGER PRIMARY KEY, name TEXT, code TEXT, category_id INTEGER, purchase_price REAL)`,
		`CREATE TABLE tenant_contacts (id INTEGER PRIMARY KEY, business_name TEXT, doc_number TEXT)`,
		`CREATE TABLE tenant_sales (id INTEGER PRIMARY KEY, status TEXT, issue_date DATETIME, doc_type TEXT, series TEXT, number TEXT,
			contact_id INTEGER, branch_id INTEGER, user_id INTEGER, sale_origin TEXT, issued_from_nota_sale_id INTEGER, currency TEXT, exchange_rate REAL, deleted_at DATETIME)`,
		`CREATE TABLE tenant_sale_items (id INTEGER PRIMARY KEY, sale_id INTEGER, product_id INTEGER, description TEXT, quantity REAL, unit_price REAL,
			subtotal REAL, line_discount_subtotal REAL, global_discount_subtotal REAL, purchase_price REAL)`,
		`INSERT INTO tenant_products VALUES (1,'Producto A','A',1,0),(2,'Producto B','B',1,0)`,
		`INSERT INTO tenant_sales VALUES
			(1,'paid','2026-10-02 10:00:00','BOLETA','B001','1',NULL,1,1,'direct',NULL,'PEN',NULL,NULL),
			(2,'paid','2026-10-03 10:00:00','BOLETA','B001','2',NULL,1,2,'direct',NULL,'PEN',NULL,NULL),
			(3,'cancelled','2026-10-03 11:00:00','BOLETA','B001','3',NULL,1,1,'direct',NULL,'PEN',NULL,NULL)`,
		// usuario 1: A x2 a 10 (costo 4) y B x1 a 20 (sin costo) · usuario 2: A x1 a 10 (costo 4) · anulada: no cuenta
		`INSERT INTO tenant_sale_items VALUES
			(1,1,1,'A',2,10,20,0,0,4),(2,1,2,'B',1,20,20,0,0,0),(3,2,1,'A',1,10,10,0,0,4),(4,3,1,'A',5,10,50,0,0,4)`,
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC)
	svc := NewSaleService(db)

	_, all, err := svc.ProfitDetail(ProfitDetailParams{DateFrom: &from, DateTo: &to})
	if err != nil {
		t.Fatal(err)
	}
	if !approx(all.TotalSales, 50) || !approx(all.TotalCost, 12) || !approx(all.TotalProfit, 38) || all.LinesWithoutCost != 1 {
		t.Fatalf("todos: %+v", all)
	}
	// Ingreso - costo = utilidad, siempre.
	if !approx(all.TotalSales-all.TotalCost, all.TotalProfit) {
		t.Fatalf("la utilidad no cuadra: %+v", all)
	}

	_, u1, _ := svc.ProfitDetail(ProfitDetailParams{DateFrom: &from, DateTo: &to, UserID: 1})
	if !approx(u1.TotalSales, 40) || !approx(u1.TotalCost, 8) {
		t.Fatalf("usuario 1: %+v", u1)
	}
	_, pa, _ := svc.ProfitDetail(ProfitDetailParams{DateFrom: &from, DateTo: &to, ProductID: 1})
	if !approx(pa.TotalSales, 30) || !approx(pa.TotalCost, 12) || pa.LinesWithoutCost != 0 {
		t.Fatalf("producto A: %+v", pa)
	}
	_, both, _ := svc.ProfitDetail(ProfitDetailParams{DateFrom: &from, DateTo: &to, ProductID: 1, UserID: 2})
	if !approx(both.TotalSales, 10) || !approx(both.TotalCost, 4) {
		t.Fatalf("producto A + usuario 2: %+v", both)
	}
}

// Utilidad de una venta en USD: el precio se lleva a soles con el TC de la venta antes de restar el costo (que está en soles).
func TestProfitDetail_USDSaleConvertedToSoles(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:profit_usd?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE tenant_products (id INTEGER PRIMARY KEY, name TEXT, code TEXT, category_id INTEGER, purchase_price REAL)`,
		`CREATE TABLE tenant_contacts (id INTEGER PRIMARY KEY, business_name TEXT, doc_number TEXT)`,
		`CREATE TABLE tenant_sales (id INTEGER PRIMARY KEY, status TEXT, issue_date DATETIME, doc_type TEXT, series TEXT, number TEXT,
			contact_id INTEGER, branch_id INTEGER, user_id INTEGER, sale_origin TEXT, issued_from_nota_sale_id INTEGER, currency TEXT, exchange_rate REAL, deleted_at DATETIME)`,
		`CREATE TABLE tenant_sale_items (id INTEGER PRIMARY KEY, sale_id INTEGER, product_id INTEGER, description TEXT, quantity REAL, unit_price REAL,
			subtotal REAL, line_discount_subtotal REAL, global_discount_subtotal REAL, purchase_price REAL)`,
		`INSERT INTO tenant_products VALUES (1,'Producto A','A',1,0)`,
		`INSERT INTO tenant_sales VALUES (1,'paid','2026-10-02 10:00:00','BOLETA','B001','1',NULL,1,1,'direct',NULL,'USD',4,NULL)`,
		// US$ 100 por unidad, costo S/ 150 → utilidad S/ 250 por unidad (no -50).
		`INSERT INTO tenant_sale_items VALUES (1,1,1,'A',1,100,100,0,0,150)`,
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	_, sum, err := NewSaleService(db).ProfitDetail(ProfitDetailParams{})
	if err != nil {
		t.Fatal(err)
	}
	if !approx(sum.TotalSales, 400) || !approx(sum.TotalCost, 150) || !approx(sum.TotalProfit, 250) {
		t.Fatalf("utilidad USD: %+v (quiere ventas 400, costo 150, utilidad 250)", sum)
	}
}
