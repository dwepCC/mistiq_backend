package service

import "testing"

func TestNormalizeRelatedDocType(t *testing.T) {
	cases := map[string]string{
		"01":      "01",
		"Factura": "01",
		"FACTURA": "01",
		"Boleta":  "03",
		"03":      "03",
		"NC":      "07",
		"Nota de crédito": "07",
		"Ticket":          "12",
	}
	for in, want := range cases {
		if got := normalizeRelatedDocType(in); got != want {
			t.Errorf("normalizeRelatedDocType(%q) = %q, want %q", in, got, want)
		}
	}
}
