package salecontext

import "testing"

func TestAutoSuggestIGVRetention(t *testing.T) {
	contact := &ContactSnapshot{
		DocType:             "6",
		DocNumber:           "20100070970",
		EsAgenteDeRetencion: true,
	}
	if !AutoSuggestIGVRetention("01", contact, 800, "PEN", nil) {
		t.Fatal("expected auto suggest for factura + agente de retención")
	}
	if AutoSuggestIGVRetention("03", contact, 800, "PEN", nil) {
		t.Fatal("boleta should not auto suggest")
	}
	if !AutoSuggestIGVRetention("01", contact, 0, "PEN", nil) {
		t.Fatal("auto suggest no depende del monto (umbral 700 al evaluar/guardar)")
	}
	if !AutoSuggestIGVRetention("01", contact, 700, "PEN", nil) {
		t.Fatal("total igual 700 debe preseleccionar si es agente de retención")
	}
	rate := 3.5
	if !AutoSuggestIGVRetention("01", contact, 201, "USD", &rate) {
		t.Fatal("USD con agente de retención debe preseleccionar sin evaluar umbral aquí")
	}
	perc := &ContactSnapshot{
		DocType:              "6",
		DocNumber:            "20100070970",
		EsAgenteDeRetencion:  true,
		EsAgenteDePercepcion: true,
	}
	if AutoSuggestIGVRetention("01", perc, 900, "PEN", nil) {
		t.Fatal("agente percepción excluido")
	}
}

func TestEvaluateIGVRetention_applicable(t *testing.T) {
	contact := &ContactSnapshot{
		DocType:             "6",
		DocNumber:           "20100070970",
		EsAgenteDeRetencion: true,
	}
	res := EvaluateIGVRetention(RetentionEvalInput{
		RequestedRetention: true,
		SunatDocCode:       "01",
		Contact:            contact,
		SaleTotal:          1000,
		Currency:           "PEN",
	})
	if !res.Applicable {
		t.Fatalf("expected applicable, reason=%s", res.Reason)
	}
	if res.ObligationAmount != 30 {
		t.Fatalf("expected 30 retention, got %v", res.ObligationAmount)
	}
	if res.NetCollectible != 970 {
		t.Fatalf("expected net 970, got %v", res.NetCollectible)
	}
}

func TestEvaluateIGVRetention_usdThreshold(t *testing.T) {
	contact := &ContactSnapshot{
		DocType:             "6",
		DocNumber:           "20100070970",
		EsAgenteDeRetencion: true,
	}
	rate := 3.5
	res := EvaluateIGVRetention(RetentionEvalInput{
		RequestedRetention: true,
		SunatDocCode:       "01",
		Contact:            contact,
		SaleTotal:          250,
		Currency:           "USD",
		ExchangeRate:       &rate,
	})
	if !res.Applicable {
		t.Fatalf("expected applicable for USD 250 * 3.5 > 700, reason=%s", res.Reason)
	}
}

func TestEvaluateIGVRetention_manualOverrideBelowThreshold(t *testing.T) {
	contact := &ContactSnapshot{DocType: "6", DocNumber: "20100070970"}
	res := EvaluateIGVRetention(RetentionEvalInput{
		RequestedRetention: true,
		ManualOverride:     true,
		SunatDocCode:       "01",
		Contact:            contact,
		SaleTotal:          500,
	})
	if res.Applicable {
		t.Fatal("below threshold should not be applicable even if requested")
	}
	if !res.HasIgvRetention {
		t.Fatal("flag should remain on when user requested")
	}
}

func TestEvaluateIGVRetention_disabled(t *testing.T) {
	contact := &ContactSnapshot{DocType: "6", DocNumber: "20100070970", EsAgenteDeRetencion: true}
	res := EvaluateIGVRetention(RetentionEvalInput{
		RequestedRetention: false,
		ManualOverride:     true,
		SunatDocCode:       "01",
		Contact:            contact,
		SaleTotal:          1500,
	})
	if res.Applicable {
		t.Fatal("expected not applicable when disabled")
	}
	if res.ObligationAmount != 0 {
		t.Fatalf("expected zero obligation, got %v", res.ObligationAmount)
	}
}

func TestResolveRequestedRetention_nilInput(t *testing.T) {
	contact := &ContactSnapshot{DocType: "6", EsAgenteDeRetencion: true}
	req, manual := ResolveRequestedRetention(nil, "01", contact, 900, "PEN", nil)
	if !req || manual {
		t.Fatalf("expected auto suggest without manual, got req=%v manual=%v", req, manual)
	}
}

func TestNormalizeReferenceInput(t *testing.T) {
	row, ok := normalizeReferenceInput(FiscalReferenceInput{
		ReferenceKind:        RefKindGuiaRemitente,
		ReferencedFullNumber: "T001-00000001",
	})
	if !ok {
		t.Fatal("expected ok")
	}
	if row.ReferencedSunatType != "09" {
		t.Fatalf("expected sunat 09, got %s", row.ReferencedSunatType)
	}
	if row.ReferencedSeries != "T001" || row.ReferencedNumber != "00000001" {
		t.Fatalf("parsed series/number wrong: %s %s", row.ReferencedSeries, row.ReferencedNumber)
	}
}
