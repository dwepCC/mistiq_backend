package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Caso real (tenant 20612257320, FC02-30 sobre F002-1065): línea de 177 (150 + IGV 27), anticipo de 30 deducido → factura de 147.
// La nota copiaba las líneas brutas y declaraba 147: SUNAT 3280. Se verifica sobre el payload FINAL que arma buildNotePayload.
func TestBuildNotePayload_FacturaConAnticipo_LineasNetasCuadranConElTotal(t *testing.T) {
	db := setupNoteDocumentTestDB(t)
	svc := &BillingService{db: db}
	now := time.Now()
	db.Create(&database.TenantCompanyConfig{RUC: "20123456789", BusinessName: "EMPRESA SAC", TradeName: "EMPRESA", Address: "Av. Test 123", Ubigeo: "150101", SunatEnabled: true, TaxRate: 18})
	cid := uint(1)
	db.Create(&database.TenantContact{ID: cid, DocType: "RUC", DocNumber: "10193238314", BusinessName: "CLIENTE", Address: "Jr 1", Ubigeo: "150101"})
	db.Create(&database.TenantDocumentSeries{ID: 1, BranchID: 1, Category: "factura", SunatCode: "01", Series: "F002", Active: true})
	db.Create(&database.TenantDocumentSeries{ID: 2, BranchID: 1, Category: "nota_credito", SunatCode: "07", Series: "FC02", Active: true})
	orig := database.TenantSale{BranchID: 1, ContactID: &cid, SeriesID: 1, DocType: "FACTURA", Series: "F002", Correlative: 1065, Number: "F002-00001065",
		IssueDate: now, Subtotal: 124.58, TaxAmount: 22.42, Total: 147, Currency: "PEN", Status: "paid", BillingStatus: "accepted", OperationTypeCode: "0101"}
	db.Create(&orig)
	line := database.TenantSaleItem{Code: "14GMAZ", Description: "Sistema de facturación", Unit: "NIU", Quantity: 1, UnitPrice: 177, TaxRate: 18,
		IgvAffectationType: "10", Subtotal: 150, TaxAmount: 27, Total: 177}
	line.SaleID = orig.ID
	db.Create(&line)
	oid := orig.ID
	nc := database.TenantSale{BranchID: 1, ContactID: &cid, SeriesID: 2, DocType: "NOTA_CREDITO", Series: "FC02", Correlative: 30, Number: "FC02-00000030",
		IssueDate: now, Subtotal: 124.58, TaxAmount: 22.42, Total: 147, Currency: "PEN", Status: "paid", BillingStatus: "pending",
		OriginalSaleID: &oid, NoteReasonCode: "01", Notes: "error"}
	db.Create(&nc)
	ncLine := line
	ncLine.ID, ncLine.SaleID = 0, nc.ID
	db.Create(&ncLine)

	p, err := svc.buildNotePayload(nc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !near(p.MtoImpVenta, 147) || !near(p.MtoOperGravadas, 124.58) || !near(p.MtoIGV, 22.42) || !near(p.ValorVenta, 124.58) {
		t.Fatalf("cabecera: importe=%.2f gravadas=%.2f igv=%.2f valorVenta=%.2f, quería 147 / 124.58 / 22.42 / 124.58", p.MtoImpVenta, p.MtoOperGravadas, p.MtoIGV, p.ValorVenta)
	}
	if len(p.Details) != 1 {
		t.Fatalf("detalles: %d", len(p.Details))
	}
	d := p.Details[0]
	if !near(d.MtoValorVenta, 124.58) || !near(d.MtoBaseIgv, 124.58) || !near(d.Igv, 22.42) || !near(d.MtoPrecioUnitario, 147) {
		t.Fatalf("línea: valorVenta=%.2f base=%.2f igv=%.2f precio=%.2f", d.MtoValorVenta, d.MtoBaseIgv, d.Igv, d.MtoPrecioUnitario)
	}
	// Lo que SUNAT recalcula: base + IGV de la cabecera = importe total.
	if !near(p.MtoOperGravadas+p.MtoIGV, p.MtoImpVenta) {
		t.Fatalf("base + IGV = %.2f ≠ importe %.2f (SUNAT 3280)", p.MtoOperGravadas+p.MtoIGV, p.MtoImpVenta)
	}
	// La venta y sus líneas guardadas no se tocan.
	var stored database.TenantSaleItem
	db.First(&stored, line.ID)
	if !near(stored.Subtotal, 150) {
		t.Fatalf("la línea guardada no debía cambiar: %.2f", stored.Subtotal)
	}
}

func TestNetNoteItems_SinDescuentoNoCambiaNada(t *testing.T) {
	items := []database.TenantSaleItem{gravadaItem(1, "A", 1, 118), exoneradaItem(2, "B", 1, 50)}
	note := &database.TenantSale{Subtotal: 150, TaxAmount: 18, Total: 168}
	got := netNoteItemsForPayload(items, note)
	for i := range items {
		if !near(got[i].Subtotal, items[i].Subtotal) || !near(got[i].TaxAmount, items[i].TaxAmount) {
			t.Fatalf("línea %d cambió sin necesidad: %+v", i, got[i])
		}
	}
}

func TestNetNoteItems_DescuentoGlobalRepartidoYExacto(t *testing.T) {
	// Dos líneas gravadas (100 + 200 base, IGV 18 + 36) con un descuento global que deja base 270 e IGV 48.60.
	a := gravadaItem(1, "A", 1, 118)
	b := gravadaItem(2, "B", 1, 236)
	a.GlobalDiscountSubtotal, b.GlobalDiscountSubtotal = 10, 20
	note := &database.TenantSale{Subtotal: 270, TaxAmount: 48.60, Total: 318.60}
	got := netNoteItemsForPayload([]database.TenantSaleItem{a, b}, note)
	var sub, tax float64
	for _, it := range got {
		sub += it.Subtotal
		tax += it.TaxAmount
		if it.GlobalDiscountSubtotal != 0 {
			t.Fatalf("el descuento global ya va aplicado, no debe quedar en la línea: %+v", it)
		}
	}
	if !near(sub, 270) || !near(tax, 48.60) {
		t.Fatalf("las líneas suman base %.2f / IGV %.2f, quería 270 / 48.60", sub, tax)
	}
}
