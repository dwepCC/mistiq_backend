package docseries

import "testing"

func TestCreditNoteSeriesPrefixForAffected(t *testing.T) {
	if CreditNoteSeriesPrefixForAffected("FACTURA", "") != "FC" {
		t.Fatal("factura")
	}
	if CreditNoteSeriesPrefixForAffected("BOLETA", "03") != "BC" {
		t.Fatal("boleta")
	}
	if CreditNoteSeriesPrefixForAffected("", "01") != "FC" {
		t.Fatal("sunat 01")
	}
}

func TestValidateNotaCreditoSeriesCode(t *testing.T) {
	for _, ok := range []string{"FC01", "BC02", "fc01"} {
		if err := ValidateNotaCreditoSeriesCode(ok); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"F001", "FD01", "FC1", "XX01"} {
		if err := ValidateNotaCreditoSeriesCode(bad); err == nil {
			t.Fatalf("expected error for %s", bad)
		}
	}
}
