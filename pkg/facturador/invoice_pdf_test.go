package facturador

import (
	"encoding/json"
	"testing"
)

func TestMarshalInvoicePDFBody_withPayloadParameters(t *testing.T) {
	payload := &InvoicePayload{
		TipoDoc: "01",
		Parameters: &InvoicePDFParameters{
			User: InvoicePDFUserParameters{
				Extras: []InvoicePDFExtra{
					{Name: "Retención IGV (3%)", Value: "S/ 24.00"},
					{Name: "Neto a cobrar", Value: "S/ 776.00"},
				},
			},
		},
	}
	raw, err := marshalInvoicePDFBody(payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	params, ok := body["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("missing parameters: %+v", body)
	}
	user, ok := params["user"].(map[string]any)
	if !ok {
		t.Fatalf("missing user: %+v", params)
	}
	extras, ok := user["extras"].([]any)
	if !ok || len(extras) != 2 {
		t.Fatalf("extras: %+v", user["extras"])
	}
}

func TestMarshalInvoicePDFBody_withExtras(t *testing.T) {
	payload := &InvoicePayload{TipoDoc: "01", Serie: "F001", Correlativo: "1"}
	opts := &InvoicePDFOptions{
		Extras: []InvoicePDFExtra{
			{Name: "Retención IGV (3%)", Value: "S/ 30.00"},
			{Name: "Neto a cobrar", Value: "S/ 970.00"},
		},
	}
	raw, err := marshalInvoicePDFBody(payload, opts)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	params, ok := body["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("missing parameters: %+v", body)
	}
	user, ok := params["user"].(map[string]any)
	if !ok {
		t.Fatalf("missing user: %+v", params)
	}
	extras, ok := user["extras"].([]any)
	if !ok || len(extras) != 2 {
		t.Fatalf("extras: %+v", user["extras"])
	}
}

func TestMarshalInvoicePDFBody_noExtras(t *testing.T) {
	payload := &InvoicePayload{TipoDoc: "01"}
	raw, err := marshalInvoicePDFBody(payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["parameters"]; ok {
		t.Fatal("parameters should be omitted")
	}
}
