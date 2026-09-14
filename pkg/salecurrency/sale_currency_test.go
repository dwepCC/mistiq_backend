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
	code3, err := NormalizeOperationType("0401")
	if err != nil || code3 != OpVentasNoDomiciliados {
		t.Fatalf("expected 0401, got %q err=%v", code3, err)
	}
	// 0201/2001 vecinos que siguen fuera de alcance (no forman parte de este esfuerzo).
	if _, err := NormalizeOperationType("0201"); err == nil {
		t.Fatal("export de servicios (0201) debe seguir rechazado")
	}
	if _, err := NormalizeOperationType("2001"); err == nil {
		t.Fatal("percepción (2001) debe seguir rechazada")
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

func TestRequireExchangeRateForUSD(t *testing.T) {
	if err := RequireExchangeRateForUSD(CurrencyPEN, nil); err != nil {
		t.Fatalf("PEN should not require TC: %v", err)
	}
	if err := RequireExchangeRateForUSD(CurrencyUSD, nil); err == nil {
		t.Fatal("expected error for USD without TC")
	}
	rate := 3.75
	if err := RequireExchangeRateForUSD(CurrencyUSD, &rate); err != nil {
		t.Fatalf("expected ok with TC: %v", err)
	}
}
