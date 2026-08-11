package salecontext

import (
	"strings"
	"testing"

	"tukifac/pkg/facturador"
)

func TestEnrichmentFromOutput_andApply(t *testing.T) {
	out := &FiscalContextOutput{
		Profile: FiscalProfileOutput{
			OperationTypeCode:   "0101",
			FiscalObservations:  "Entrega parcial",
			PurchaseOrderNumber: "OC-999",
			HasIgvRetention:     true,
			ShowTermsConditions: true,
		},
		References: []FiscalReferenceOutput{
			{
				ReferenceKind:        RefKindGuiaRemitente,
				ReferencedSunatType:  "09",
				ReferencedFullNumber: "T001-00000001",
			},
		},
		Summary: FiscalSummaryOutput{
			SaleTotal: 1000,
		},
	}
	e := EnrichmentFromOutput(out)
	if e == nil || e.Compra != "OC-999" || e.Observacion != "Entrega parcial" {
		t.Fatalf("unexpected enrichment: %+v", e)
	}
	if len(e.Guias) != 1 || e.Guias[0].NroDoc != "T001-00000001" {
		t.Fatalf("guias: %+v", e.Guias)
	}

	payload := &facturador.InvoicePayload{TipoOperacion: "0101", TipoMoneda: "PEN"}
	ApplyToInvoicePayload(payload, e)
	if payload.Compra != "OC-999" || payload.Observacion != "Entrega parcial" {
		t.Fatalf("payload fields: compra=%q obs=%q", payload.Compra, payload.Observacion)
	}
	if len(payload.Guias) != 1 || payload.Guias[0].NroDoc != "T001-00000001" {
		t.Fatalf("payload guias: %+v", payload.Guias)
	}

	e.RetentionApplied = true
	e.RetentionAmount = 30
	e.NetCollectible = 970
	ApplyToInvoicePayload(&facturador.InvoicePayload{TipoMoneda: "PEN"}, e)
	payload2 := &facturador.InvoicePayload{TipoMoneda: "PEN"}
	ApplyToInvoicePayload(payload2, e)
	if !strings.Contains(payload2.Observacion, "Retención IGV (3%)") || !strings.Contains(payload2.Observacion, "S/ 30.00") {
		t.Fatalf("retention obs: %q", payload2.Observacion)
	}
	if payload2.Parameters == nil || len(payload2.Parameters.User.Extras) != 2 {
		t.Fatalf("retention pdf parameters: %+v", payload2.Parameters)
	}
	extras := BuildIGVRetentionPDFExtras(30, 970, "PEN")
	if len(extras) != 2 || extras[0].Value != "S/ 30.00" {
		t.Fatalf("extras: %+v", extras)
	}
}

func TestApplyToInvoicePayload_nilSafe(t *testing.T) {
	ApplyToInvoicePayload(nil, &InvoiceEnrichment{})
	ApplyToInvoicePayload(&facturador.InvoicePayload{}, nil)
}

