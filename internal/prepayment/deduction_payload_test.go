package prepayment

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	sunatpre "tukifac/pkg/sunat/prepayment"
)

type ublLegalMonetaryTotal struct {
	LineExtensionAmount string `xml:"LineExtensionAmount"`
	TaxInclusiveAmount  string `xml:"TaxInclusiveAmount"`
	PrepaidAmount       string `xml:"PrepaidAmount"`
	PayableAmount       string `xml:"PayableAmount"`
}

type ublTaxTotal struct {
	TaxAmount string `xml:"TaxAmount"`
}

type ublInvoiceDeduction struct {
	TaxTotal           ublTaxTotal           `xml:"TaxTotal"`
	LegalMonetaryTotal ublLegalMonetaryTotal `xml:"LegalMonetaryTotal"`
}

func TestApplyDeductionToInvoicePayload_MatchesLegacyPHP(t *testing.T) {
	groupTotals := sunatpre.SaleGroupTotals{
		GravadoSubtotal: 100,
		GravadoTax:      18,
		GravadoTotal:    118,
		Subtotal:        100,
		TaxAmount:       18,
		Total:           118,
	}
	applyRes, err := sunatpre.ApplyDeductionToSaleTotals(
		sunatpre.AffectationGravado,
		groupTotals,
		50,
		59,
		18,
	)
	if err != nil {
		t.Fatal(err)
	}

	payload := &facturador.InvoicePayload{
		SubTotal:    118,
		MtoImpVenta: 118,
	}
	apps := []database.TenantSalePrepaymentApplication{
		{
			RelatedDocType:   "02",
			DocumentNumber:   "F001-13",
			AffectationGroup: sunatpre.AffectationGravado,
			Amount:           50,
			Total:            59,
		},
	}
	ApplyDeductionToInvoicePayload(payload, apps, applyRes, groupTotals, SaleDeductionNet{
		Subtotal: 50, TaxAmount: 9, Total: 59,
	})

	if payload.ValorVenta != 100 {
		t.Fatalf("valorVenta bruto: got %v want 100", payload.ValorVenta)
	}
	if payload.MtoIGV != 9 {
		t.Fatalf("mtoIGV neto: got %v want 9", payload.MtoIGV)
	}
	if payload.TotalImpuestos != 9 {
		t.Fatalf("totalImpuestos neto: got %v want 9", payload.TotalImpuestos)
	}
	if payload.SubTotal != 118 {
		t.Fatalf("subTotal bruto: got %v want 118", payload.SubTotal)
	}
	if payload.MtoImpVenta != 59 {
		t.Fatalf("mtoImpVenta neto: got %v want 59", payload.MtoImpVenta)
	}
	if payload.TotalAnticipos != 59 {
		t.Fatalf("totalAnticipos: got %v want 59", payload.TotalAnticipos)
	}
	if payload.MtoOperGravadas != 50 {
		t.Fatalf("mtoOperGravadas: got %v want 50", payload.MtoOperGravadas)
	}
	if len(payload.Descuentos) != 1 || payload.Descuentos[0].CodTipo != sunatpre.DiscountCodeGravadoAnticipo {
		t.Fatalf("descuento 04 esperado, got %+v", payload.Descuentos)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"totalAnticipos":59`) {
		t.Fatalf("json debe usar totalAnticipos (Greenter), got %s", string(raw))
	}
	if strings.Contains(string(raw), `"mtoAnticipos"`) {
		t.Fatalf("json no debe usar mtoAnticipos: %s", string(raw))
	}
}

func TestApplyDeductionToInvoicePayload_UsesSaleNetTaxWhenGrossMismatch(t *testing.T) {
	// Simula billing donde ítems no cuadran con deducción pero la venta guardó totales netos válidos.
	grossTotals := sunatpre.SaleGroupTotals{
		GravadoSubtotal: 50,
		Subtotal:        50,
	}
	applyRes, err := sunatpre.ApplyDeductionToSaleTotals(
		sunatpre.AffectationGravado,
		grossTotals,
		100,
		118,
		18,
	)
	if err != nil {
		t.Fatal(err)
	}
	if applyRes.Totals.GravadoTax >= 0 {
		t.Fatalf("applyResult tax should be negative internally, got %v", applyRes.Totals.GravadoTax)
	}

	apps := []database.TenantSalePrepaymentApplication{
		{RelatedDocType: "02", DocumentNumber: "F001-1", AffectationGroup: sunatpre.AffectationGravado, Amount: 100, Total: 118},
	}
	payload := &facturador.InvoicePayload{}
	ApplyDeductionToInvoicePayload(payload, apps, applyRes, grossTotals, SaleDeductionNet{
		Subtotal: 10, TaxAmount: 1.8, Total: 11.8,
	})

	if payload.MtoIGV < 0 || payload.TotalImpuestos < 0 {
		t.Fatalf("impuestos negativos: igv=%v totalImp=%v", payload.MtoIGV, payload.TotalImpuestos)
	}
	if payload.MtoIGV != 1.8 {
		t.Fatalf("mtoIGV=%v want 1.8 from sale header", payload.MtoIGV)
	}
	if payload.MtoImpVenta != 11.8 {
		t.Fatalf("mtoImpVenta=%v want 11.8", payload.MtoImpVenta)
	}
}

func TestGreenterXML_PrepaymentDeduction_PayableAmountPositive(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not in PATH")
	}

	groupTotals := sunatpre.SaleGroupTotals{
		GravadoSubtotal: 173.73,
		GravadoTax:      31.27,
		GravadoTotal:    205,
		Subtotal:        173.73,
		TaxAmount:       31.27,
		Total:           205,
	}
	applyRes, err := sunatpre.ApplyDeductionToSaleTotals(
		sunatpre.AffectationGravado,
		groupTotals,
		173.73,
		205,
		18,
	)
	if err != nil {
		t.Fatal(err)
	}

	payload := facturador.InvoicePayload{
		UBLVersion:      "2.1",
		TipoOperacion:   "0101",
		TipoDoc:         "01",
		Serie:           "F001",
		Correlativo:     "99",
		FechaEmision:    "2026-07-08T12:00:00-05:00",
		FormaPago:       &facturador.InvoiceFormaPago{Tipo: "Contado"},
		TipoMoneda:      "PEN",
		MtoOperGravadas: 173.73,
		MtoIGV:          31.27,
		TotalImpuestos:  31.27,
		ValorVenta:      173.73,
		SubTotal:        205,
		MtoImpVenta:     205,
		Details: []facturador.InvoiceDetail{
			{
				Unidad: "NIU", Cantidad: 1, CodProducto: "P01", Descripcion: "Producto demo",
				MtoValorUnitario: 173.73, MtoValorVenta: 173.73, TipAfeIgv: "10",
				MtoBaseIgv: 173.73, PorcentajeIgv: 18, Igv: 31.27, TotalImpuestos: 31.27,
				MtoPrecioUnitario: 205,
			},
		},
		Company: facturador.InvoiceCompany{
			RUC: "10726187938", RazonSocial: "DEMO S.A.C.", NombreComercial: "DEMO",
			Address: facturador.InvoiceAddress{Ubigueo: "040101", CodigoPais: "PE", Direccion: "Arequipa"},
		},
		Client: facturador.InvoiceClient{
			TipoDoc: "6", NumDoc: "20100070970", RznSocial: "CLIENTE SAC",
			Address: facturador.InvoiceAddress{Ubigueo: "040101", CodigoPais: "PE", Direccion: "Arequipa"},
		},
	}
	apps := []database.TenantSalePrepaymentApplication{
		{RelatedDocType: "02", DocumentNumber: "F001-13", AffectationGroup: sunatpre.AffectationGravado, Amount: 173.73, Total: 205},
	}
	ApplyDeductionToInvoicePayload(&payload, apps, applyRes, groupTotals, SaleDeductionNet{
		Subtotal: 0, TaxAmount: 0, Total: 0,
	})

	xmlBytes, err := renderGreenterInvoiceXML(t, payload)
	if err != nil {
		t.Fatal(err)
	}
	var doc ublInvoiceDeduction
	if err := xml.Unmarshal(xmlBytes, &doc); err != nil {
		t.Fatalf("parse xml: %v\n%s", err, string(xmlBytes))
	}
	lmt := doc.LegalMonetaryTotal
	if lmt.LineExtensionAmount != "173.73" {
		t.Fatalf("LineExtensionAmount=%q want 173.73 (valor venta bruto)", lmt.LineExtensionAmount)
	}
	if strings.HasPrefix(lmt.LineExtensionAmount, "-") {
		t.Fatalf("LineExtensionAmount no puede ser negativo: %q", lmt.LineExtensionAmount)
	}
	if lmt.PayableAmount != "0.00" {
		t.Fatalf("PayableAmount=%q want 0.00 (deducción total)", lmt.PayableAmount)
	}
	if strings.HasPrefix(lmt.PayableAmount, "-") {
		t.Fatalf("PayableAmount no puede ser negativo: %q", lmt.PayableAmount)
	}
	if lmt.TaxInclusiveAmount != "205.00" {
		t.Fatalf("TaxInclusiveAmount=%q want 205.00", lmt.TaxInclusiveAmount)
	}
	if lmt.PrepaidAmount != "205.00" {
		t.Fatalf("PrepaidAmount=%q want 205.00", lmt.PrepaidAmount)
	}
	if doc.TaxTotal.TaxAmount != "0.00" {
		t.Fatalf("TaxAmount=%q want 0.00", doc.TaxTotal.TaxAmount)
	}
	if strings.HasPrefix(doc.TaxTotal.TaxAmount, "-") {
		t.Fatalf("TaxAmount no puede ser negativo: %q", doc.TaxTotal.TaxAmount)
	}
}

func TestGreenterXML_PrepaymentDeduction_PartialNetPayable(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not in PATH")
	}

	groupTotals := sunatpre.SaleGroupTotals{
		GravadoSubtotal: 199.15,
		GravadoTax:      35.85,
		GravadoTotal:    235,
		Subtotal:        199.15,
		TaxAmount:       35.85,
		Total:           235,
	}
	applyRes, err := sunatpre.ApplyDeductionToSaleTotals(
		sunatpre.AffectationGravado,
		groupTotals,
		173.73,
		205,
		18,
	)
	if err != nil {
		t.Fatal(err)
	}

	payload := facturador.InvoicePayload{
		UBLVersion:    "2.1",
		TipoOperacion: "0101",
		TipoDoc:       "01",
		Serie:         "F001",
		Correlativo:   "100",
		FechaEmision:  "2026-07-08T12:00:00-05:00",
		FormaPago:     &facturador.InvoiceFormaPago{Tipo: "Contado"},
		TipoMoneda:    "PEN",
		Details: []facturador.InvoiceDetail{
			{
				Unidad: "NIU", Cantidad: 1, CodProducto: "P01", Descripcion: "Producto demo",
				MtoValorUnitario: 199.15, MtoValorVenta: 199.15, TipAfeIgv: "10",
				MtoBaseIgv: 199.15, PorcentajeIgv: 18, Igv: 35.85, TotalImpuestos: 35.85,
				MtoPrecioUnitario: 235,
			},
		},
		Company: facturador.InvoiceCompany{
			RUC: "10726187938", RazonSocial: "DEMO S.A.C.", NombreComercial: "DEMO",
			Address: facturador.InvoiceAddress{Ubigueo: "040101", CodigoPais: "PE", Direccion: "Arequipa"},
		},
		Client: facturador.InvoiceClient{
			TipoDoc: "6", NumDoc: "20100070970", RznSocial: "CLIENTE SAC",
			Address: facturador.InvoiceAddress{Ubigueo: "040101", CodigoPais: "PE", Direccion: "Arequipa"},
		},
	}
	apps := []database.TenantSalePrepaymentApplication{
		{RelatedDocType: "02", DocumentNumber: "F001-13", AffectationGroup: sunatpre.AffectationGravado, Amount: 173.73, Total: 205},
	}
	ApplyDeductionToInvoicePayload(&payload, apps, applyRes, groupTotals, SaleDeductionNet{
		Subtotal: 25.42, TaxAmount: 4.58, Total: 30,
	})

	xmlBytes, err := renderGreenterInvoiceXML(t, payload)
	if err != nil {
		t.Fatal(err)
	}
	var doc ublInvoiceDeduction
	if err := xml.Unmarshal(xmlBytes, &doc); err != nil {
		t.Fatalf("parse xml: %v\n%s", err, string(xmlBytes))
	}
	lmt := doc.LegalMonetaryTotal
	if lmt.LineExtensionAmount != "199.15" {
		t.Fatalf("LineExtensionAmount=%q want 199.15 (valor venta bruto)", lmt.LineExtensionAmount)
	}
	if strings.HasPrefix(lmt.LineExtensionAmount, "-") {
		t.Fatalf("LineExtensionAmount no puede ser negativo: %q", lmt.LineExtensionAmount)
	}
	if lmt.PayableAmount != "30.00" {
		t.Fatalf("PayableAmount=%q want 30.00", lmt.PayableAmount)
	}
	if lmt.TaxInclusiveAmount != "235.00" {
		t.Fatalf("TaxInclusiveAmount=%q want 235.00 (bruto legacy PHP)", lmt.TaxInclusiveAmount)
	}
	if lmt.PrepaidAmount != "205.00" {
		t.Fatalf("PrepaidAmount=%q want 205.00", lmt.PrepaidAmount)
	}
	if doc.TaxTotal.TaxAmount != "4.58" {
		t.Fatalf("TaxAmount=%q want 4.58", doc.TaxTotal.TaxAmount)
	}
	if strings.HasPrefix(doc.TaxTotal.TaxAmount, "-") {
		t.Fatalf("TaxAmount no puede ser negativo: %q", doc.TaxTotal.TaxAmount)
	}
}

func renderGreenterInvoiceXML(t *testing.T, payload facturador.InvoicePayload) ([]byte, error) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, os.ErrInvalid
	}
	script := filepath.Join(filepath.Dir(thisFile), "..", "billing", "service", "testdata", "render_greenter_invoice.php")
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("php", script)
	cmd.Stdin = strings.NewReader(string(body))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	return out, nil
}
