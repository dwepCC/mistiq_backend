package service

import (
	"errors"
	"math"
	"testing"
	"time"

	"tukifac/internal/exportacion"
	"tukifac/pkg/database"
	"tukifac/pkg/salecurrency"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

func near(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func setServiceCharge(t *testing.T, db *gorm.DB, enabled bool, rate float64) {
	t.Helper()
	if err := db.Model(&database.TenantCompanyConfig{}).Where("id = 1").
		Updates(map[string]interface{}{"service_charge_enabled": enabled, "service_charge_rate": rate}).Error; err != nil {
		t.Fatal(err)
	}
}

// El recargo al consumo se calcula sobre el valor de venta sin IGV, no toca el IGV y suma al total a pagar.
func TestCreate_RecargoAlConsumoSobreValorDeVentaSinIGV(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	contactID := seedClienteConRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")
	setServiceCharge(t, db, true, 10)

	// S/ 118 con IGV = 100 + 18. Recargo 10 % de 100 = 10 → total a pagar 128.
	in := maxMontoSaleInput(contactID, seriesID, "01", 118)
	in.Payments = []PaymentInput{{Method: "cash", Amount: 128}}
	in.ApplyServiceCharge = true
	sale, err := svc.Create(in)
	if err != nil {
		t.Fatalf("venta con recargo: %v", err)
	}
	if !near(sale.Subtotal, 100) || !near(sale.TaxAmount, 18) {
		t.Errorf("el recargo no debe tocar base ni IGV: subtotal=%v igv=%v", sale.Subtotal, sale.TaxAmount)
	}
	if !near(sale.ServiceChargeAmount, 10) || !near(sale.ServiceChargeRate, 10) {
		t.Errorf("recargo: monto=%v tasa=%v", sale.ServiceChargeAmount, sale.ServiceChargeRate)
	}
	if !near(sale.Total, 128) {
		t.Errorf("total a pagar = %v, quiero 128", sale.Total)
	}
}

// El cajero elige cobrarlo o no; sin la marca no hay recargo aunque el tenant lo tenga activado.
func TestCreate_SinMarcaNoCobraRecargo(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	contactID := seedClienteConRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")
	setServiceCharge(t, db, true, 10)

	sale, err := svc.Create(maxMontoSaleInput(contactID, seriesID, "01", 118))
	if err != nil {
		t.Fatal(err)
	}
	if sale.ServiceChargeAmount != 0 || sale.ServiceChargeRate != 0 || !near(sale.Total, 118) {
		t.Errorf("sin marca no hay recargo: %+v", sale)
	}
}

// Pedir recargo cuando el tenant no lo activó se rechaza (una pantalla desactualizada no debe cobrarlo).
func TestCreate_RecargoConTenantApagado_SeRechaza(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	contactID := seedClienteConRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")

	in := maxMontoSaleInput(contactID, seriesID, "01", 118)
	in.ApplyServiceCharge = true
	if _, err := svc.Create(in); !errors.Is(err, tax.ErrServiceChargeDisabled) {
		t.Fatalf("esperaba ErrServiceChargeDisabled, got %v", err)
	}
}

// Un % configurado fuera del tope legal (dato corrupto) nunca se cobra.
func TestCreate_RecargoConPorcentajeFueraDeTope_SeRechaza(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	contactID := seedClienteConRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")
	setServiceCharge(t, db, true, 20)

	in := maxMontoSaleInput(contactID, seriesID, "01", 118)
	in.Payments = []PaymentInput{{Method: "cash", Amount: 200}}
	in.ApplyServiceCharge = true
	if _, err := svc.Create(in); err == nil {
		t.Fatal("un % sobre el tope de 13 debió rechazarse")
	}
}

// La nota de venta (00) también puede llevarlo: es el comprobante interno de muchos restaurantes.
func TestCreate_RecargoEnNotaDeVenta(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	contactID := seedClienteSinRUC(t, db)
	seriesID := seedSeries(t, db, "Nota de venta", "00", "NV01")
	setServiceCharge(t, db, true, 10)

	in := maxMontoSaleInput(contactID, seriesID, "00", 118)
	in.Payments = []PaymentInput{{Method: "cash", Amount: 128}}
	in.ApplyServiceCharge = true
	sale, err := svc.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	if !near(sale.ServiceChargeAmount, 10) || !near(sale.Total, 128) {
		t.Errorf("nota de venta con recargo: %+v", sale)
	}
}

// Una nota de venta que ya cobró recargo conserva el mismo recargo al emitir su factura/boleta electrónica, aunque el tenant lo haya apagado
// desde entonces: el comprobante debe coincidir con lo que el cliente ya pagó.
func TestIssueElectronicFromNota_ConservaElRecargoCobrado(t *testing.T) {
	db := setupIssueElectronicFromNotaTestDB(t)
	notaID, boletaSeriesID := seedNotaVentaUSD(t, db, true)
	if err := db.Model(&database.TenantSale{}).Where("id = ?", notaID).Updates(map[string]interface{}{
		"currency": "PEN", "exchange_rate": nil, "total": 128, "service_charge_rate": 10, "service_charge_amount": 10,
	}).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&database.TenantSalePayment{}).Where("sale_id = ?", notaID).Update("amount", 128)

	child, err := NewSaleService(db).IssueElectronicFromNota(notaID, boletaSeriesID, 1, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !near(child.ServiceChargeAmount, 10) || !near(child.ServiceChargeRate, 10) || !near(child.Total, 128) || !near(child.TaxAmount, 18) {
		t.Fatalf("la boleta debe repetir el recargo de la nota: %+v", child)
	}
}

// El resumen de ventas informa aparte el recargo incluido en el total.
func TestSummaryStats_InformaElRecargoAparte(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	contactID := seedClienteConRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")
	setServiceCharge(t, db, true, 10)
	in := maxMontoSaleInput(contactID, seriesID, "01", 118)
	in.Payments = []PaymentInput{{Method: "cash", Amount: 128}}
	in.ApplyServiceCharge = true
	if _, err := svc.Create(in); err != nil {
		t.Fatal(err)
	}
	st := svc.SummaryStats(0, time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour))
	if st["count"].(int64) != 1 || !near(st["total"].(float64), 128) || !near(st["service_charge_total"].(float64), 10) {
		t.Fatalf("resumen: %+v", st)
	}
}

// El recargo al consumo no se combina con la exportación de servicios (todavía sin probar ese cruce contra SUNAT).
func TestCreate_RecargoConExportacion_SeRechaza(t *testing.T) {
	db, c, s := setupExportTestDB(t, true)
	setServiceCharge(t, db, true, 10)
	in := exportInput(c, s, salecurrency.OpExportServicios, &exportacion.SaleInput{CountryUse: "US"})
	in.ApplyServiceCharge = true
	if _, err := NewSaleService(db).Create(in); err == nil {
		t.Fatal("recargo + exportación debió rechazarse")
	}
}
