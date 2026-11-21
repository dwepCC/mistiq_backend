package service

import (
	"strings"
	"testing"
)

func TestNormalizePaymentRefs(t *testing.T) {
	got, err := normalizePaymentRefs(nil)
	if err != nil || got != "" {
		t.Fatalf("sin métodos debe dar vacío, got %q err %v", got, err)
	}

	// Filas sin método se descartan; el monto se redondea; la referencia se recorta.
	got, err = normalizePaymentRefs([]QuotationPaymentRef{
		{Method: " cash ", Amount: 10.005, Reference: "  r1 "},
		{Method: "", Amount: 5},
		{Method: "yape"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"method":"cash"`) || !strings.Contains(got, `"method":"yape"`) || strings.Contains(got, `"amount":5`) {
		t.Fatalf("serialización inesperada: %s", got)
	}

	if _, err := normalizePaymentRefs([]QuotationPaymentRef{{Method: "cash", Amount: -1}}); err == nil {
		t.Fatal("un monto negativo debe rechazarse")
	}
	if _, err := normalizePaymentRefs([]QuotationPaymentRef{{Method: strings.Repeat("x", 31)}}); err == nil {
		t.Fatal("un código de método demasiado largo debe rechazarse")
	}
	many := make([]QuotationPaymentRef, 7)
	for i := range many {
		many[i] = QuotationPaymentRef{Method: "cash"}
	}
	if _, err := normalizePaymentRefs(many); err == nil {
		t.Fatal("más de 6 métodos debe rechazarse")
	}
}

func TestPaymentRefsToPrintRoundTrip(t *testing.T) {
	raw, err := normalizePaymentRefs([]QuotationPaymentRef{{Method: "cash", Amount: 50}, {Method: "yape", Amount: 25.5, Reference: "op-1"}})
	if err != nil {
		t.Fatal(err)
	}
	rows := paymentRefsToPrint(raw)
	if len(rows) != 2 || rows[0].Method != "cash" || rows[1].Amount != 25.5 || rows[1].Reference != "op-1" {
		t.Fatalf("round trip incorrecto: %+v", rows)
	}
	if paymentRefsToPrint("") != nil || paymentRefsToPrint("no es json") != nil {
		t.Fatal("entrada vacía o inválida debe dar nil")
	}
}
