package salecurrency

import "testing"

func TestNormalizeCurrency(t *testing.T) {
	c, err := NormalizeCurrency("usd")
	if err != nil || c != CurrencyUSD {
		t.Fatalf("expected USD, got %q err=%v", c, err)
	}
	if _, err := NormalizeCurrency("EUR"); err == nil {
	 t.Fatal("expected error for EUR")
	}
}

func TestNormalizeOperationType(t *testing.T) {
	code, err := NormalizeOperationType("")
	if err != nil || code != OpVentaInterna {
		t.Fatalf("expected 0101 default, got %q", code)
	}
	if _, err := NormalizeOperationType("0200"); err == nil {
		t.Fatal("export should be rejected")
	}
	code2, err := NormalizeOperationType("1001")
	if err != nil || code2 != OpDetraccion {
		t.Fatalf("expected 1001, got %q err=%v", code2, err)
	}
}

func TestTotalInPEN(t *testing.T) {
	rate := 3.5
	got := TotalInPEN(CurrencyUSD, 200, &rate)
	if got != 700 {
		t.Fatalf("expected 700 PEN equivalent, got %v", got)
	}
	if TotalInPEN(CurrencyPEN, 500, nil) != 500 {
		t.Fatal("PEN should pass through")
	}
}
