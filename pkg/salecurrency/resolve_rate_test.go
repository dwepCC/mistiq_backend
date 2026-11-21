package salecurrency

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/dailyfx"
)

func withLookup(t *testing.T, f func(day string) (float64, bool, bool)) {
	t.Helper()
	prev := dailyfx.Lookup
	dailyfx.Lookup = f
	t.Cleanup(func() { dailyfx.Lookup = prev })
}

func TestResolveDocumentRate_FillsUSDFromSavedDailyRate(t *testing.T) {
	withLookup(t, func(day string) (float64, bool, bool) {
		if day == "2026-11-10" {
			return 3.431, false, true
		}
		return 0, false, false
	})
	issue := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	cur, rate, est, err := ResolveDocumentRate("usd", nil, issue)
	if err != nil || cur != "USD" || rate == nil || *rate != 3.431 || est {
		t.Fatalf("USD sin TC debe tomar el guardado del día: %v %v %v %v", cur, rate, est, err)
	}
	// El TC que envía el usuario se respeta (es editable).
	own := 3.5
	if _, r, _, _ := ResolveDocumentRate("USD", &own, issue); r == nil || *r != 3.5 {
		t.Errorf("el TC enviado manda: %v", r)
	}
	// PEN: sin TC aunque lo envíen.
	if c, r, _, _ := ResolveDocumentRate("", &own, issue); c != "PEN" || r != nil {
		t.Errorf("PEN no lleva TC: %v %v", c, r)
	}
	// Día sin TC guardado: error claro.
	_, _, _, err = ResolveDocumentRate("USD", nil, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "tipo de cambio") {
		t.Errorf("sin TC del día debe fallar: %v", err)
	}
	// Moneda no soportada / TC inválido.
	if _, _, _, err := ResolveDocumentRate("EUR", nil, issue); err == nil {
		t.Error("EUR no soportada")
	}
	bad := -1.0
	if _, _, _, err := ResolveDocumentRate("USD", &bad, issue); err == nil {
		t.Error("TC negativo debe rechazarse")
	}
}

func TestResolveDocumentRate_UsesLimaDayAndKeepsLegacyWithoutProvider(t *testing.T) {
	var got string
	withLookup(t, func(day string) (float64, bool, bool) { got = day; return 3.4, true, true })
	// 02:00 UTC del 11-nov es todavía el 10-nov en Lima (UTC-5).
	_, _, est, err := ResolveDocumentRate("USD", nil, time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC))
	if err != nil || got != "2026-11-10" || !est {
		t.Errorf("día de Lima = %q estimado=%v err=%v", got, est, err)
	}
	dailyfx.Lookup = nil
	if _, r, _, err := ResolveDocumentRate("USD", nil, time.Now()); err != nil || r != nil {
		t.Errorf("sin proveedor de TC se conserva el comportamiento anterior (TC opcional): %v %v", r, err)
	}
}
