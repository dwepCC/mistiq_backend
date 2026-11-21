package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Una factura con recargo al consumo: el total (128) incluye el recargo (10), pero base (100) e IGV (18) no. El registro de ventas lo muestra en
// «otros tributos y cargos» y el asiento lo manda al pasivo con los trabajadores, nunca a ingresos; el asiento cuadra.
func TestServiceCharge_RegistroYAsiento(t *testing.T) {
	db := testDB(t)
	cli := database.TenantContact{DocType: "RUC", DocNumber: "20555555551", BusinessName: "CLIENTE SAC", Type: "customer"}
	db.Create(&cli)
	sale := mkSale(t, db, saleOpts{docType: "FACTURA", series: "F001", corr: 1, subtotal: 100, tax: 18, contact: &cli.ID,
		items: []database.TenantSaleItem{item("10", 100, 18)}})
	if err := db.Model(&sale).Updates(map[string]interface{}{"service_charge_amount": 10, "service_charge_rate": 10, "total": 128}).Error; err != nil {
		t.Fatal(err)
	}
	s := New(db)
	docs, warns, err := s.SalesDocs(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(docs) != 1 {
		t.Fatalf("docs=%d err=%v", len(docs), err)
	}
	b := docs[0].PEN
	if b.Taxed != 100 || b.Igv != 18 || b.OtherCharges != 10 || b.Total != 128 {
		t.Fatalf("desglose: %+v", b)
	}

	vs, vwarns := SalesVouchers(docs, DefaultAccounts)
	if len(vs) != 1 || !VoucherBalanced(vs[0]) || len(vwarns) != 0 || len(warns) != 0 {
		t.Fatalf("asiento: %+v warns=%v %v", vs, vwarns, warns)
	}
	var charge, taxed float64
	for _, l := range vs[0].Lines {
		if l.Concept == ConceptServiceCharge {
			charge = l.Amount
			if l.Account != ServiceChargeAccount || l.Side != "H" {
				t.Errorf("el recargo va al pasivo (41), no a ingresos: %+v", l)
			}
		}
		if l.Concept == ConceptTaxed {
			taxed = l.Amount
		}
	}
	if charge != 10 || taxed != 100 {
		t.Errorf("recargo=%v ingresos=%v (deben ser 10 y 100)", charge, taxed)
	}

	tb, err := s.SalesRegister("2026-03", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.Rows) != 1 || tb.Rows[0][16] != 10.0 || tb.Rows[0][17] != 128.0 || tb.Footer[0][16] != 10.0 || tb.Footer[0][17] != 128.0 {
		t.Fatalf("registro: %v / %v", tb.Rows, tb.Footer)
	}
}
