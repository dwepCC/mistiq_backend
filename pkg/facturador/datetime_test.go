package facturador

import (
	"testing"
	"time"
)

func TestFormatFiscalDateTime(t *testing.T) {
	loc := limaLocation()
	in := time.Date(2026, 5, 24, 12, 0, 0, 0, loc)
	got := FormatFiscalDateTime(in)
	want := "2026-05-24T12:00:00-05:00"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// Caso real que rechazó SUNAT con código 2108 ("Presentación fuera de fecha o con Fecha/hora
// mayor a la recepción en SUNAT"): una guía de remisión emitida a las 08:35 llegaba con
// fechaEmision normalizada a 12:00:00 (mediodía) — una hora "futura" respecto al envío real.
// FormatFiscalDateTimeExact debe preservar la hora real, sin normalizar a mediodía.
func TestFormatFiscalDateTimeExact_preservesRealTime(t *testing.T) {
	loc := limaLocation()
	in := time.Date(2026, 8, 26, 8, 35, 6, 0, loc)
	got := FormatFiscalDateTimeExact(in)
	want := "2026-08-26T08:35:06-05:00"
	if got != want {
		t.Fatalf("expected %q, got %q — no debe normalizar a mediodía", want, got)
	}
}

// Confirma que la normalización a mediodía sigue existiendo para facturas/boletas
// (FormatFiscalDateTime, sin tocar) — FormatFiscalDateTimeExact es la excepción, no el default.
func TestFormatFiscalDateTime_stillNormalizesToNoon(t *testing.T) {
	loc := limaLocation()
	in := time.Date(2026, 8, 26, 8, 35, 6, 0, loc)
	got := FormatFiscalDateTime(in)
	want := "2026-08-26T12:00:00-05:00"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
