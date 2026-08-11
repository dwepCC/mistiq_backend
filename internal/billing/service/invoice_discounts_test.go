package service

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	"tukifac/pkg/tax"
)

func testNormUnit(u string) string {
	if u == "" {
		return "NIU"
	}
	return u
}

func TestBuildInvoiceDetails_LineDiscountOnly(t *testing.T) {
	// Greenter factura-descuento-linea: bruto 20, neto 16, unitario bruto 2.
	items := []database.TenantSaleItem{{
		Code: "P001", Description: "Prod", Unit: "NIU", Quantity: 10,
		IgvAffectationType: "10", TaxRate: 18,
		Subtotal: 180, TaxAmount: 32.4, Total: 212.4,
		LineDiscountSubtotal: 20,
	}}
	details, err := BuildInvoiceDetailsFromSaleItems(items, 18, testNormUnit)
	if err != nil {
		t.Fatal(err)
	}
	d := details[0]
	if d.MtoValorUnitario != 20 {
		t.Fatalf("mtoValorUnitario=%v want 20 (bruto unitario)", d.MtoValorUnitario)
	}
	if d.MtoValorVenta != 180 || d.MtoBaseIgv != 180 || d.Igv != 32.4 {
		t.Fatalf("post-line: venta=%v base=%v igv=%v", d.MtoValorVenta, d.MtoBaseIgv, d.Igv)
	}
	if d.MtoPrecioUnitario != 21.24 {
		t.Fatalf("mtoPrecioUnitario=%v want 21.24", d.MtoPrecioUnitario)
	}
	if len(d.Descuentos) != 1 || d.Descuentos[0].CodTipo != tax.AllowanceCodeLineDiscountAffectsIGV {
		t.Fatalf("descuentos=%+v", d.Descuentos)
	}
	if d.Descuentos[0].MontoBase != 200 || d.Descuentos[0].Monto != 20 {
		t.Fatalf("allowance=%+v", d.Descuentos[0])
	}
}

func TestBuildInvoiceDetails_GlobalDiscountOnly(t *testing.T) {
	// Caso auditoría: precio 500 IGV incl., desc. global 50.
	items := []database.TenantSaleItem{{
		Code: "P001", Description: "Prod", Unit: "NIU", Quantity: 1,
		IgvAffectationType: "10", TaxRate: 18,
		Subtotal: 373.73, TaxAmount: 67.27, Total: 441,
		GlobalDiscountSubtotal: 50,
	}}
	details, err := BuildInvoiceDetailsFromSaleItems(items, 18, testNormUnit)
	if err != nil {
		t.Fatal(err)
	}
	d := details[0]
	if d.MtoValorVenta != 423.73 || d.MtoBaseIgv != 423.73 {
		t.Fatalf("pre-global venta/base=%v/%v want 423.73", d.MtoValorVenta, d.MtoBaseIgv)
	}
	if d.Igv != 76.27 {
		t.Fatalf("igv=%v want 76.27 (pre-global)", d.Igv)
	}
	if d.MtoPrecioUnitario != 500 {
		t.Fatalf("mtoPrecioUnitario=%v want 500", d.MtoPrecioUnitario)
	}
	if d.MtoValorUnitario != 423.73 {
		t.Fatalf("mtoValorUnitario=%v want 423.73", d.MtoValorUnitario)
	}

	sale := &database.TenantSale{GlobalDiscountAmount: 50}
	charges, sumOtros := BuildGlobalInvoiceDiscounts(sale, items)
	if sumOtros != 0 {
		t.Fatalf("sumOtrosDescuentos=%v want 0 for cod 02", sumOtros)
	}
	if len(charges) != 1 || charges[0].CodTipo != tax.AllowanceCodeGlobalDiscountAffectsIGV {
		t.Fatalf("charges=%+v", charges)
	}
	if charges[0].MontoBase != 423.73 || charges[0].Monto != 50 {
		t.Fatalf("global charge=%+v", charges[0])
	}
}

func TestBuildInvoiceDetails_LineAndGlobalCombined(t *testing.T) {
	items := []database.TenantSaleItem{{
		Code: "P001", Description: "Prod", Unit: "NIU", Quantity: 1,
		IgvAffectationType: "10", TaxRate: 18,
		Subtotal: 81, TaxAmount: 14.58, Total: 95.58,
		LineDiscountSubtotal: 10, GlobalDiscountSubtotal: 9,
	}}
	details, err := BuildInvoiceDetailsFromSaleItems(items, 18, testNormUnit)
	if err != nil {
		t.Fatal(err)
	}
	d := details[0]
	if d.MtoValorUnitario != 100 {
		t.Fatalf("mtoValorUnitario=%v want 100", d.MtoValorUnitario)
	}
	if d.MtoValorVenta != 90 || d.MtoBaseIgv != 90 || d.Igv != 16.2 {
		t.Fatalf("pre-global: venta=%v base=%v igv=%v want 90/90/16.2", d.MtoValorVenta, d.MtoBaseIgv, d.Igv)
	}
	if d.MtoPrecioUnitario != 106.2 {
		t.Fatalf("mtoPrecioUnitario=%v want 106.2", d.MtoPrecioUnitario)
	}
	if len(d.Descuentos) != 1 || d.Descuentos[0].Monto != 10 {
		t.Fatalf("line allowance=%+v", d.Descuentos[0])
	}
}

func TestBuildInvoiceDetails_Affectacion15GravadoBonificaciones(t *testing.T) {
	// SUNAT Cat. 07 código 15: gravado bonificaciones — mismo IGV que 10, tipAfeIgv distinto en XML.
	items := []database.TenantSaleItem{{
		Code: "BONIF01", Description: "Bonificación gravada", Unit: "NIU", Quantity: 1,
		IgvAffectationType: "15", TaxRate: 18,
		Subtotal: 100, TaxAmount: 18, Total: 118,
	}}
	details, err := BuildInvoiceDetailsFromSaleItems(items, 18, testNormUnit)
	if err != nil {
		t.Fatal(err)
	}
	d := details[0]
	if d.TipAfeIgv != "15" {
		t.Fatalf("tipAfeIgv=%q want 15", d.TipAfeIgv)
	}
	if d.MtoBaseIgv != 100 || d.Igv != 18 || d.PorcentajeIgv != 18 {
		t.Fatalf("base/igv/pct=%v/%v/%v want 100/18/18", d.MtoBaseIgv, d.Igv, d.PorcentajeIgv)
	}
	if d.MtoPrecioUnitario != 0 || d.MtoValorUnitario != 0 || d.MtoValorGratuito != 100 {
		t.Fatalf("precio/unit/gratuito=%v/%v/%v want 0/0/100", d.MtoPrecioUnitario, d.MtoValorUnitario, d.MtoValorGratuito)
	}
	if d.MtoValorVenta != 100 {
		t.Fatalf("mtoValorVenta=%v want 100 (base referencial línea)", d.MtoValorVenta)
	}
	if d.TotalImpuestos != 0 {
		t.Fatalf("totalImpuestos=%v want 0 (legacy total_taxes; IGV referencial en igv)", d.TotalImpuestos)
	}
}

func TestBuildInvoiceDetails_Mixed10And15_LegacyTaxStructure(t *testing.T) {
	// Escenario alineado con B001-57 / B002-171: línea 15 gratuita + línea 10 onerosa.
	items := []database.TenantSaleItem{
		{
			Code: "LBC22", Description: "Revisión General de Fluidos", Unit: "NIU", Quantity: 1,
			IgvAffectationType: "15", TaxRate: 18,
			Subtotal: 29.66, TaxAmount: 5.34, Total: 0,
		},
		{
			Code: "LBC21", Description: "Cambio de Filtro de Aire", Unit: "NIU", Quantity: 1,
			IgvAffectationType: "10", TaxRate: 18,
			Subtotal: 21.19, TaxAmount: 3.81, Total: 25,
		},
	}
	details, err := BuildInvoiceDetailsFromSaleItems(items, 18, testNormUnit)
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 2 {
		t.Fatalf("details len=%d want 2", len(details))
	}

	free := details[0]
	if free.TipAfeIgv != "15" {
		t.Fatalf("free tipAfeIgv=%q", free.TipAfeIgv)
	}
	if free.TotalImpuestos != 0 {
		t.Fatalf("free totalImpuestos=%v want 0", free.TotalImpuestos)
	}
	if free.Igv != 5.34 || free.MtoBaseIgv != 29.66 || free.MtoValorVenta != 29.66 {
		t.Fatalf("free referential=%v/%v/%v want 29.66/5.34/29.66", free.MtoValorVenta, free.MtoBaseIgv, free.Igv)
	}
	if free.MtoValorGratuito != 29.66 || free.MtoValorUnitario != 0 {
		t.Fatalf("free price ref=%v/%v want gratuito 29.66 unit 0", free.MtoValorGratuito, free.MtoValorUnitario)
	}

	onerous := details[1]
	if onerous.TipAfeIgv != "10" {
		t.Fatalf("onerous tipAfeIgv=%q", onerous.TipAfeIgv)
	}
	if onerous.TotalImpuestos != 3.81 {
		t.Fatalf("onerous totalImpuestos=%v want 3.81", onerous.TotalImpuestos)
	}
	if onerous.Igv != 3.81 || onerous.MtoBaseIgv != 21.19 {
		t.Fatalf("onerous igv/base=%v/%v want 3.81/21.19", onerous.Igv, onerous.MtoBaseIgv)
	}
	if onerous.MtoValorGratuito != 0 {
		t.Fatalf("onerous mtoValorGratuito=%v want 0", onerous.MtoValorGratuito)
	}

	tot := ComputeInvoiceSunatTotals(items, 25)
	if tot.TotalImpuestos != 3.81 || tot.MtoIGVGratuitas != 5.34 {
		t.Fatalf("header taxes=%v igvFree=%v want 3.81/5.34", tot.TotalImpuestos, tot.MtoIGVGratuitas)
	}
}

func TestBuildInvoiceDetails_GravadoRetiroPremio11_ChargeableTaxZero(t *testing.T) {
	items := []database.TenantSaleItem{{
		Code: "P11", Description: "Retiro premio", Unit: "NIU", Quantity: 1,
		IgvAffectationType: "11", TaxRate: 18,
		Subtotal: 50, TaxAmount: 9, Total: 0,
	}}
	details, err := BuildInvoiceDetailsFromSaleItems(items, 18, testNormUnit)
	if err != nil {
		t.Fatal(err)
	}
	if details[0].TotalImpuestos != 0 {
		t.Fatalf("totalImpuestos=%v want 0 for code 11", details[0].TotalImpuestos)
	}
	if details[0].Igv != 9 {
		t.Fatalf("igv=%v want 9 referential", details[0].Igv)
	}
}

func TestBuildInvoiceDetails_MixedThreeProducts(t *testing.T) {
	items := []database.TenantSaleItem{
		{
			Code: "A", Description: "Gravado", Unit: "NIU", Quantity: 1,
			IgvAffectationType: "10", TaxRate: 18,
			Subtotal: 83.08, TaxAmount: 14.95, Total: 98.03,
			LineDiscountSubtotal: 10, GlobalDiscountSubtotal: 6.92,
		},
		{
			Code: "B", Description: "Exonerado", Unit: "NIU", Quantity: 2,
			IgvAffectationType: "20", TaxRate: 0,
			Subtotal: 129.23, TaxAmount: 0, Total: 129.23,
			LineDiscountSubtotal: 20, GlobalDiscountSubtotal: 10.77,
		},
		{
			Code: "C", Description: "Gravado IGV incl.", Unit: "NIU", Quantity: 1,
			IgvAffectationType: "10", TaxRate: 18,
			Subtotal: 87.69, TaxAmount: 15.78, Total: 103.47,
			LineDiscountSubtotal: 5, GlobalDiscountSubtotal: 7.31,
		},
	}
	details, err := BuildInvoiceDetailsFromSaleItems(items, 18, testNormUnit)
	if err != nil {
		t.Fatal(err)
	}

	var sumPreGlobal, sumLineIGVPre float64
	want := []struct {
		valorVenta, baseIgv, igv, valorUnit, precioUnit float64
	}{
		{90, 90, 16.2, 100, 106.2},
		{140, 140, 0, 80, 70},
		{95, 95, 17.1, 100, 112.1},
	}
	for i, d := range details {
		w := want[i]
		if d.MtoValorVenta != w.valorVenta || d.MtoBaseIgv != w.baseIgv || d.Igv != w.igv {
			t.Fatalf("L%d pre-global venta/base/igv=%v/%v/%v want %v/%v/%v",
				i+1, d.MtoValorVenta, d.MtoBaseIgv, d.Igv, w.valorVenta, w.baseIgv, w.igv)
		}
		if d.MtoValorUnitario != w.valorUnit || d.MtoPrecioUnitario != w.precioUnit {
			t.Fatalf("L%d unit/precio=%v/%v want %v/%v", i+1, d.MtoValorUnitario, d.MtoPrecioUnitario, w.valorUnit, w.precioUnit)
		}
		sumPreGlobal += d.MtoValorVenta
		sumLineIGVPre += d.Igv
	}

	sale := &database.TenantSale{GlobalDiscountAmount: 25}
	charges, sumOtros := BuildGlobalInvoiceDiscounts(sale, items)
	if sumOtros != 0 {
		t.Fatalf("sumOtros=%v want 0", sumOtros)
	}
	if charges[0].MontoBase != 325 || charges[0].Monto != 25 {
		t.Fatalf("global=%+v want base 325 monto 25", charges[0])
	}

	var sumFinalSub, sumFinalTax float64
	for _, it := range items {
		sumFinalSub += it.Subtotal
		sumFinalTax += it.TaxAmount
	}
	if math.Abs(sumPreGlobal-charges[0].MontoBase) > 0.01 {
		t.Fatalf("sum pre-global=%v base charge=%v", sumPreGlobal, charges[0].MontoBase)
	}
	if math.Abs(sumPreGlobal-charges[0].Monto-sumFinalSub) > 0.02 {
		t.Fatalf("valorVenta doc=%v want %v", sumFinalSub, sumPreGlobal-charges[0].Monto)
	}
	// IGV doc = suma PRE-global − IGV sobre cuotas gravadas del global
	globalIGVRed := round2(6.92*0.18 + 7.31*0.18)
	if math.Abs(sumLineIGVPre-globalIGVRed-sumFinalTax) > 0.02 {
		t.Fatalf("igv doc=%v calc=%v (pre=%v red=%v)", sumFinalTax, sumLineIGVPre-globalIGVRed, sumLineIGVPre, globalIGVRed)
	}
}

func TestBuildGlobalInvoiceDiscounts_NoSumOtrosDescuentosInJSON(t *testing.T) {
	sale := &database.TenantSale{GlobalDiscountAmount: 50}
	items := []database.TenantSaleItem{{
		Subtotal: 373.73, GlobalDiscountSubtotal: 50,
	}}
	_, sumOtros := BuildGlobalInvoiceDiscounts(sale, items)
	if sumOtros != 0 {
		t.Fatalf("sumOtros=%v", sumOtros)
	}
	payload := facturador.InvoicePayload{
		SumOtrosDescuentos: sumOtros,
		MtoImpVenta:        441,
	}
	b, _ := json.Marshal(payload)
	if strings.Contains(string(b), "sumOtrosDescuentos") {
		t.Fatalf("JSON should omit sumOtrosDescuentos: %s", string(b))
	}
}
