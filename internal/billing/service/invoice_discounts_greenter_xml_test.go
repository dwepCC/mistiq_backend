package service

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
)

// ublInvoiceLineTax mirrors InvoiceLine/TaxTotal for Greenter XML assertions.
type ublInvoiceLineTax struct {
	TaxAmount   string `xml:"TaxAmount"`
	TaxSubtotal struct {
		TaxableAmount string `xml:"TaxableAmount"`
		TaxAmount     string `xml:"TaxAmount"`
		TaxCategory   struct {
			Percent                string `xml:"Percent"`
			TaxExemptionReasonCode string `xml:"TaxExemptionReasonCode"`
			TaxScheme              struct {
				ID          string `xml:"ID"`
				Name        string `xml:"Name"`
				TaxTypeCode string `xml:"TaxTypeCode"`
			} `xml:"TaxScheme"`
		} `xml:"TaxCategory"`
	} `xml:"TaxSubtotal"`
}

type ublInvoiceLine struct {
	LineExtensionAmount string `xml:"LineExtensionAmount"`
	TaxTotal            ublInvoiceLineTax `xml:"TaxTotal"`
}

type ublInvoice struct {
	InvoiceLines []ublInvoiceLine `xml:"InvoiceLine"`
}

func TestGreenterXML_Mixed10And15_LineTaxTotalMatchesLegacy(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not in PATH; skip Greenter XML integration test")
	}

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
	tot := ComputeInvoiceSunatTotals(items, 25)

	payload := facturador.InvoicePayload{
		UBLVersion:       "2.1",
		TipoOperacion:    "0101",
		TipoDoc:          "03",
		Serie:            "B001",
		Correlativo:      "57",
		FechaEmision:     "2026-07-08T12:00:00-05:00",
		FormaPago:        &facturador.InvoiceFormaPago{Tipo: "Contado"},
		TipoMoneda:       "PEN",
		MtoOperGravadas:  tot.MtoOperGravadas,
		MtoOperGratuitas: tot.MtoOperGratuitas,
		MtoIGVGratuitas:  tot.MtoIGVGratuitas,
		MtoIGV:           tot.MtoIGV,
		TotalImpuestos:   tot.TotalImpuestos,
		ValorVenta:       tot.ValorVenta,
		SubTotal:         tot.MtoImpVenta,
		MtoImpVenta:      tot.MtoImpVenta,
		Details:          details,
		Company: facturador.InvoiceCompany{
			RUC: "10726187938", RazonSocial: "DEMO S.A.C.", NombreComercial: "DEMO",
			Address: facturador.InvoiceAddress{Ubigueo: "040101", CodigoPais: "PE", Direccion: "Arequipa"},
		},
		Client: facturador.InvoiceClient{
			TipoDoc: "0", NumDoc: "99999999999", RznSocial: "Clientes Varios",
			Address: facturador.InvoiceAddress{Ubigueo: "040101", CodigoPais: "PE", Direccion: "Arequipa"},
		},
	}

	xmlBytes, err := renderGreenterInvoiceXML(t, payload)
	if err != nil {
		t.Fatal(err)
	}

	var doc ublInvoice
	if err := xml.Unmarshal(xmlBytes, &doc); err != nil {
		t.Fatalf("parse xml: %v\n%s", err, string(xmlBytes))
	}
	if len(doc.InvoiceLines) != 2 {
		t.Fatalf("lines=%d want 2", len(doc.InvoiceLines))
	}

	free := doc.InvoiceLines[0].TaxTotal
	if free.TaxAmount != "0.00" {
		t.Fatalf("line15 TaxTotal/TaxAmount=%q want 0.00 (legacy B002-171)", free.TaxAmount)
	}
	if free.TaxSubtotal.TaxableAmount != "29.66" || free.TaxSubtotal.TaxAmount != "5.34" {
		t.Fatalf("line15 subtotal=%s/%s want 29.66/5.34", free.TaxSubtotal.TaxableAmount, free.TaxSubtotal.TaxAmount)
	}
	if free.TaxSubtotal.TaxCategory.TaxExemptionReasonCode != "15" {
		t.Fatalf("line15 code=%q", free.TaxSubtotal.TaxCategory.TaxExemptionReasonCode)
	}
	if free.TaxSubtotal.TaxCategory.TaxScheme.ID != "9996" {
		t.Fatalf("line15 scheme=%s want 9996", free.TaxSubtotal.TaxCategory.TaxScheme.ID)
	}

	onerous := doc.InvoiceLines[1].TaxTotal
	if onerous.TaxAmount != "3.81" {
		t.Fatalf("line10 TaxTotal/TaxAmount=%q want 3.81", onerous.TaxAmount)
	}
	if onerous.TaxSubtotal.TaxCategory.TaxExemptionReasonCode != "10" || onerous.TaxSubtotal.TaxCategory.TaxScheme.ID != "1000" {
		t.Fatalf("line10 tax category mismatch: code=%s scheme=%s",
			onerous.TaxSubtotal.TaxCategory.TaxExemptionReasonCode,
			onerous.TaxSubtotal.TaxCategory.TaxScheme.ID)
	}
}

func renderGreenterInvoiceXML(t *testing.T, payload facturador.InvoicePayload) ([]byte, error) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, os.ErrInvalid
	}
	script := filepath.Join(filepath.Dir(thisFile), "testdata", "render_greenter_invoice.php")
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
