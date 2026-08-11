package docusage

import (
	"encoding/json"
	"testing"
)

// El panel central envía snake_case. Sin tags JSON, encoding/json empareja sin distinguir
// mayúsculas pero no ignora los guiones bajos: documents_qty no llegaba a DocumentsQty y
// el formulario se rechazaba con "nombre y cantidad de documentos son obligatorios"
// aunque estuviera completo. is_active y sort_order se perdían igual, sin aviso.
func TestUpsertPackageInput_bindsSnakeCasePayload(t *testing.T) {
	body := []byte(`{
		"name": "Paquete 500",
		"description": "500 documentos electrónicos",
		"documents_qty": 500,
		"price": 149.9,
		"currency": "PEN",
		"is_active": true,
		"sort_order": 3
	}`)

	var in UpsertPackageInput
	if err := json.Unmarshal(body, &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if in.Name != "Paquete 500" {
		t.Errorf("Name = %q want Paquete 500", in.Name)
	}
	if in.DocumentsQty != 500 {
		t.Errorf("DocumentsQty = %d want 500 (el campo que rompía el formulario)", in.DocumentsQty)
	}
	if in.Price != 149.9 {
		t.Errorf("Price = %v want 149.9", in.Price)
	}
	if in.Currency != "PEN" {
		t.Errorf("Currency = %q want PEN", in.Currency)
	}
	if !in.IsActive {
		t.Error("IsActive = false: el paquete se creaba inactivo sin que nadie lo pidiera")
	}
	if in.SortOrder != 3 {
		t.Errorf("SortOrder = %d want 3", in.SortOrder)
	}
	if in.Description == "" {
		t.Error("Description vacía")
	}
}

// Un payload completo no debe disparar la validación de obligatorios.
func TestUpsertPackageInput_completePayloadPassesValidation(t *testing.T) {
	var in UpsertPackageInput
	if err := json.Unmarshal([]byte(`{"name":"Paquete 100","documents_qty":100}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Name == "" || in.DocumentsQty <= 0 {
		t.Fatalf("la validación rechazaría este payload: name=%q qty=%d", in.Name, in.DocumentsQty)
	}
}
