package service

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	prepaymentsvc "tukifac/internal/prepayment"
	salecontext "tukifac/internal/fiscal/salecontext"
	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	sunatpre "tukifac/pkg/sunat/prepayment"
)

// Verifica que salecontext no pise tipoOperacion configurado tras ApplyEmitToInvoicePayload.
func TestPrepaymentEmitPayload_PreservesConfiguredOpAfterFiscalEnrichment(t *testing.T) {
	sunatpre.SetEmitOperationTypeForTest(sunatpre.OpVentaInternaAnticipos)
	t.Cleanup(func() { sunatpre.SetEmitOperationTypeForTest(sunatpre.OpVentaInternaAnticipos) })

	payload := &facturador.InvoicePayload{
		TipoOperacion: sunatpre.EmitOperationTypeCode(),
		TipoDoc:       "03",
	}
	enrich := &salecontext.InvoiceEnrichment{TipoOperacion: salecontext.DefaultOperationType}
	salecontext.ApplyToInvoicePayload(payload, enrich)
	if payload.TipoOperacion != "0101" {
		t.Fatalf("salecontext should override to 0101, got %q", payload.TipoOperacion)
	}
	voucher := &database.TenantSalePrepaymentVoucher{SaleID: 99}
	prepaymentsvc.ApplyEmitToInvoicePayload(payload, voucher)
	if payload.TipoOperacion != sunatpre.EmitOperationTypeCode() {
		t.Fatalf("prepayment should restore configured op, got %q", payload.TipoOperacion)
	}
	if payload.Parameters == nil || len(payload.Parameters.User.Extras) == 0 {
		t.Fatal("expected pdf parameters for prepayment emission")
	}
}

func TestPrepaymentPayloadJSON_ContainsConfiguredTipoOperacion(t *testing.T) {
	sunatpre.SetEmitOperationTypeForTest(sunatpre.OpVentaInternaAnticipos)
	t.Cleanup(func() { sunatpre.SetEmitOperationTypeForTest(sunatpre.OpVentaInternaAnticipos) })

	payload := facturador.InvoicePayload{TipoOperacion: "0101"}
	prepaymentsvc.ApplyEmitToInvoicePayload(&payload, &database.TenantSalePrepaymentVoucher{SaleID: 1})
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := `"tipoOperacion":"` + sunatpre.EmitOperationTypeCode() + `"`
	if !strings.Contains(string(raw), want) {
		t.Fatalf("json missing %s: %s", want, string(raw))
	}
}

func TestGreenterXML_PrepaymentEmission_InvoiceTypeCodeConfigured(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not in PATH")
	}
	sunatpre.SetEmitOperationTypeForTest(sunatpre.OpVentaInternaAnticipos)
	t.Cleanup(func() { sunatpre.SetEmitOperationTypeForTest(sunatpre.OpVentaInternaAnticipos) })

	items := []database.TenantSaleItem{
		{
			Code: "SRV01", Description: "Anticipo servicio", Unit: "NIU", Quantity: 1,
			IgvAffectationType: "10", TaxRate: 18,
			Subtotal: 100, TaxAmount: 18, Total: 118,
		},
	}
	details, err := BuildInvoiceDetailsFromSaleItems(items, 18, testNormUnit)
	if err != nil {
		t.Fatal(err)
	}
	tot := ComputeInvoiceSunatTotals(items, 118)
	payload := facturador.InvoicePayload{
		UBLVersion:      "2.1",
		TipoOperacion:   "0104",
		TipoDoc:         "03",
		Serie:           "B001",
		Correlativo:     "60",
		FechaEmision:    "2026-07-08T12:00:00-05:00",
		FormaPago:       &facturador.InvoiceFormaPago{Tipo: "Contado"},
		TipoMoneda:      "PEN",
		MtoOperGravadas: tot.MtoOperGravadas,
		MtoIGV:          tot.MtoIGV,
		TotalImpuestos:  tot.TotalImpuestos,
		ValorVenta:      tot.ValorVenta,
		SubTotal:        tot.MtoImpVenta,
		MtoImpVenta:     tot.MtoImpVenta,
		Details:         details,
		Company: facturador.InvoiceCompany{
			RUC: "10726187938", RazonSocial: "DEMO S.A.C.", NombreComercial: "DEMO",
			Address: facturador.InvoiceAddress{Ubigueo: "040101", CodigoPais: "PE", Direccion: "Arequipa"},
		},
		Client: facturador.InvoiceClient{
			TipoDoc: "0", NumDoc: "99999999999", RznSocial: "Clientes Varios",
			Address: facturador.InvoiceAddress{Ubigueo: "040101", CodigoPais: "PE", Direccion: "Arequipa"},
		},
	}
	prepaymentsvc.ApplyEmitToInvoicePayload(&payload, &database.TenantSalePrepaymentVoucher{SaleID: 1})

	xmlBytes, err := renderGreenterInvoiceXML(t, payload)
	if err != nil {
		t.Fatal(err)
	}
	xmlStr := string(xmlBytes)
	wantListID := `listID="` + sunatpre.EmitOperationTypeCode() + `"`
	if !strings.Contains(xmlStr, wantListID) {
		t.Fatalf("XML must contain InvoiceTypeCode %s:\n%s", wantListID, xmlStr)
	}
	if strings.Contains(xmlStr, "PrepaidPayment") || strings.Contains(xmlStr, "PrepaidAmount") {
		t.Fatalf("emission XML must not contain prepaid deduction nodes:\n%s", xmlStr)
	}
}
