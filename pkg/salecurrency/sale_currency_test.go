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
	// Exportación de servicios: 0201, 0202 y 0208 se aceptan (el interruptor por empresa lo valida la venta); el resto de exportaciones no.
	for _, op := range []string{"0201", "0202", "0208"} {
		if c, err := NormalizeOperationType(op); err != nil || c != op || !IsExportService(c) {
			t.Fatalf("%s debe aceptarse como exportación de servicios: %q err=%v", op, c, err)
		}
	}
	if _, err := NormalizeOperationType("0203"); err == nil {
		t.Fatal("0203 (transporte de navieras) sigue fuera de alcance")
	}
	if IsExportService("0101") || IsExportService("0200") {
		t.Fatal("0101 y 0200 no son exportación de servicios")
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

func TestTotalPENSQL(t *testing.T) {
	got := TotalPENSQL("p", "pay.amount")
	want := "CASE WHEN p.currency = 'USD' AND p.exchange_rate > 0 THEN pay.amount * p.exchange_rate ELSE pay.amount END"
	if got != want {
		t.Errorf("got %q", got)
	}
	if TotalPENSQL("", "total") != "CASE WHEN currency = 'USD' AND exchange_rate > 0 THEN total * exchange_rate ELSE total END" {
		t.Error("sin alias")
	}
}
