package service

import (
	"errors"
	"math"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"
)

// Cobro de mesa con recargo al consumo: la comanda es S/ 30 con IGV incluido (base 25.42 + IGV 4.58); el recargo es 10 % de la base (2.54) y el total a pagar sube a 32.54.
func TestBillTable_RecargoAlConsumo(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	if err := db.AutoMigrate(&database.TenantCompanyConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCompanyConfig{ID: 1, TaxRate: 18, ServiceChargeEnabled: true, ServiceChargeRate: 10}).Error; err != nil {
		t.Fatal(err)
	}
	svc := New(db)
	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	c1 := database.TenantComanda{SessionID: sess.ID, ProductCode: "P1", ProductName: "Lomo", Quantity: 1, UnitPrice: 30, Status: "pendiente", IgvAffectationType: "10"}
	if err := db.Create(&c1).Error; err != nil {
		t.Fatal(err)
	}
	series := seedSplitBillSeries(t, db)

	// Pagar solo lo de la cuenta (sin recargo) no alcanza.
	if _, err := svc.BillTable(BillInput{SessionID: sess.ID, UserID: 1, SeriesID: series.ID, DocType: "03", IssueDate: time.Now(),
		ApplyServiceCharge: true, Payments: []PaymentInput{{Method: "cash", Amount: 30}}}, tax.DefaultConfig()); err == nil {
		t.Fatal("con recargo el total es 32.54: 30.00 no debe alcanzar")
	}

	res, err := svc.BillTable(BillInput{SessionID: sess.ID, UserID: 1, SeriesID: series.ID, DocType: "03", IssueDate: time.Now(),
		ApplyServiceCharge: true, Payments: []PaymentInput{{Method: "cash", Amount: 32.54}}}, tax.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var sale database.TenantSale
	if err := db.Where("number = ?", res.Number).First(&sale).Error; err != nil {
		t.Fatal(err)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.005 }
	if !near(sale.Subtotal, 25.42) || !near(sale.TaxAmount, 4.58) || !near(sale.ServiceChargeAmount, 2.54) || !near(sale.ServiceChargeRate, 10) || !near(sale.Total, 32.54) {
		t.Errorf("venta con recargo: %+v", sale)
	}
}

func TestBillTable_RecargoConTenantApagado_SeRechaza(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	if err := db.AutoMigrate(&database.TenantCompanyConfig{}); err != nil {
		t.Fatal(err)
	}
	svc := New(db)
	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	c1 := database.TenantComanda{SessionID: sess.ID, ProductCode: "P1", ProductName: "Lomo", Quantity: 1, UnitPrice: 30, Status: "pendiente", IgvAffectationType: "10"}
	db.Create(&c1)
	series := seedSplitBillSeries(t, db)
	_, err = svc.BillTable(BillInput{SessionID: sess.ID, UserID: 1, SeriesID: series.ID, DocType: "03", IssueDate: time.Now(),
		ApplyServiceCharge: true, Payments: []PaymentInput{{Method: "cash", Amount: 40}}}, tax.DefaultConfig())
	if !errors.Is(err, tax.ErrServiceChargeDisabled) {
		t.Fatalf("esperaba ErrServiceChargeDisabled, got %v", err)
	}
}

// La precuenta estima el recargo con el mismo criterio del cobro (sobre el valor de venta sin IGV) y deja Total sin él.
func TestGetPrecuenta_EstimaElRecargo(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	if err := db.AutoMigrate(&database.TenantCompanyConfig{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantCompanyConfig{ID: 1, TaxRate: 18, ServiceChargeEnabled: true, ServiceChargeRate: 10})
	svc := New(db)
	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	ord := database.TenantTableOrder{SessionID: sess.ID, UserID: 1, OrderNumber: 1, Status: "active"}
	if err := db.Create(&ord).Error; err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantComanda{SessionID: sess.ID, OrderID: ord.ID, ProductCode: "P1", ProductName: "Lomo", Quantity: 1, UnitPrice: 30, Status: "pendiente", IgvAffectationType: "10"})
	p, err := svc.GetPrecuenta(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !p.ServiceChargeEnabled || math.Abs(p.ServiceChargeAmount-2.54) > 0.005 || math.Abs(p.Total-30) > 0.005 || math.Abs(p.TotalWithServiceCharge-32.54) > 0.005 {
		t.Fatalf("precuenta: %+v", p)
	}
}

// Al cobrar parte de la mesa, la sesión solo descuenta el consumo: el recargo va encima (98.07 − 54.77 con recargo 4.28 = 47.58, no 43.30).
func TestBilledConsumption_NoDescuentaElRecargo(t *testing.T) {
	if got := 98.07 - billedConsumption(54.77, 4.28); math.Abs(got-47.58) > 0.005 {
		t.Fatalf("quedan %v en la mesa, quiere 47.58", got)
	}
	if billedConsumption(50.49, 0) != 50.49 {
		t.Fatal("sin recargo no cambia")
	}
}
