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

func boolPtr(b bool) *bool { return &b }

// Bug reportado (Tukifac): con retención o detracción, desmarcar "¿Tiene retención de IGV?" no tenía efecto: el
// motor descartaba el valor explícito del usuario cuando no venía la marca de decisión manual (el frontend
// la omite si cree que la sugerencia ya coincide, y la fuerza a false con detracción) y volvía a aplicar la
// sugerencia automática del cliente agente de retención.
func TestEvaluateIGVRetention_ExplicitUncheckWinsOverAutoSuggestion(t *testing.T) {
	agent := &ContactSnapshot{DocType: "6", DocNumber: "20100070970", EsAgenteDeRetencion: true}
	fc := &FiscalContextInput{HasIgvRetention: boolPtr(false), IgvRetentionManualOverride: false}

	requested, manual := ResolveRequestedRetention(fc, "01", agent, 1500, "PEN", nil)
	res := EvaluateIGVRetention(RetentionEvalInput{
		RequestedRetention: requested, ManualOverride: manual,
		SunatDocCode: "01", Contact: agent, SaleTotal: 1500,
	})
	if res.HasIgvRetention || res.Applicable || res.ObligationAmount != 0 {
		t.Fatalf("el usuario desmarcó la retención: no debe aplicarse (has=%v applicable=%v amount=%v)",
			res.HasIgvRetention, res.Applicable, res.ObligationAmount)
	}
	if !res.IgvRetentionManualOverride {
		t.Fatal("desmarcar contra la sugerencia automática es una decisión manual y debe quedar registrada")
	}
}

func TestResolveRequestedRetention_ExplicitMatchingAutoIsNotAManualOverride(t *testing.T) {
	agent := &ContactSnapshot{DocType: "6", DocNumber: "20100070970", EsAgenteDeRetencion: true}
	req, manual := ResolveRequestedRetention(&FiscalContextInput{HasIgvRetention: boolPtr(true)}, "01", agent, 1500, "PEN", nil)
	if !req || manual {
		t.Fatalf("marcar lo mismo que sugiere el sistema no es un override: req=%v manual=%v", req, manual)
	}
}

// Un check encendido sin decisión manual sobre un cliente que no es agente no se convierte en retención: sigue
// mandando la regla automática (solo el check apagado es siempre explícito).
func TestResolveRequestedRetention_ExplicitCheckOnNonAgentWithoutMarkIsNotForced(t *testing.T) {
	notAgent := &ContactSnapshot{DocType: "6", DocNumber: "20100070970", EsAgenteDeRetencion: false}
	req, manual := ResolveRequestedRetention(&FiscalContextInput{HasIgvRetention: boolPtr(true)}, "01", notAgent, 1500, "PEN", nil)
	if !req || manual {
		t.Fatalf("req=%v manual=%v", req, manual)
	}
}
