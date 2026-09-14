package facturador

import "testing"

// Caso real que rechazó SUNAT con código 3006 ("descripcion de leyenda no cumple
// con el formato establecido"): tenant aarservicios (RUC 20548414424), F001-78,
// observación con un salto de línea porque el usuario tecleó un Enter.
func TestSanitizeFreeText_stripsLineBreak(t *testing.T) {
	in := "SERVICIOS PRESTADOS A LA CLÍNICA MONTESUR DURANTE EL MES DE AGOSTO 2026. MÉDICOS DE TURNO.\nOPERACIÓN SUJETA A DETRACCION DEL 12%"
	want := "SERVICIOS PRESTADOS A LA CLÍNICA MONTESUR DURANTE EL MES DE AGOSTO 2026. MÉDICOS DE TURNO. OPERACIÓN SUJETA A DETRACCION DEL 12%"
	got := SanitizeFreeText(in)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSanitizeFreeText_stripsCrLfAndTabAndCollapsesSpaces(t *testing.T) {
	in := "Servicio\r\ncon\ttab   y   espacios"
	want := "Servicio con tab y espacios"
	got := SanitizeFreeText(in)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSanitizeFreeText_leavesCleanTextUntouched(t *testing.T) {
	in := "Sin saltos de línea"
	if got := SanitizeFreeText(in); got != in {
		t.Fatalf("expected %q untouched, got %q", in, got)
	}
}

func TestSanitizeFreeText_emptyStaysEmpty(t *testing.T) {
	if got := SanitizeFreeText(""); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}
