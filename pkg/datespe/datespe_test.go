package datespe

import (
	"testing"
	"time"
)

// TestIssueTime_NoUsaElMediodiaDeLaFechaDeEmision documenta el bug que motivó este helper:
// la fecha de emisión se fija a las 12:00 a propósito (para que un cambio de zona horaria no
// mueva el documento de día), así que derivar de ella la hora imprimía «12:00:00» en TODOS
// los comprobantes. La hora tiene que salir del timestamp de creación.
func TestIssueTime_NoUsaElMediodiaDeLaFechaDeEmision(t *testing.T) {
	loc := Location()
	// Lo que se guarda como fecha de emisión: siempre mediodía.
	issueDate := time.Date(2026, 7, 20, 12, 0, 0, 0, loc)
	// Lo que ocurrió de verdad: la venta se hizo a las 15:32:50.
	createdAt := time.Date(2026, 7, 20, 15, 32, 50, 0, loc)

	got := IssueTime(createdAt)
	if got != "15:32:50" {
		t.Errorf("IssueTime = %q, want 15:32:50 (la hora real de la venta)", got)
	}
	if got == issueDate.Format("15:04:05") {
		t.Error("la hora no debe salir de la fecha de emisión: ahí siempre son las 12:00")
	}
}

// TestIssueTime_ConvierteDesdeUTC: el servidor puede correr en UTC; el comprobante debe
// mostrar la hora de Perú (UTC-5).
func TestIssueTime_ConvierteDesdeUTC(t *testing.T) {
	utc := time.Date(2026, 7, 20, 20, 32, 50, 0, time.UTC)
	got := IssueTime(utc)
	if got != "15:32:50" {
		t.Errorf("IssueTime = %q, want 15:32:50 (20:32 UTC = 15:32 en Perú)", got)
	}
}

// TestIssueTime_SinTimestampNoInventaHora: mejor sin hora que con una falsa.
func TestIssueTime_SinTimestampNoInventaHora(t *testing.T) {
	if got := IssueTime(time.Time{}); got != "" {
		t.Errorf("IssueTime = %q, want vacío", got)
	}
}

// TestIssueTime_MedianocheSeImprimeComo00: comprobar que no se cuela un formato de 12 horas,
// que mostraría «12:00:00» a medianoche y volvería a confundir con el bug original.
func TestIssueTime_MedianocheSeImprimeComo00(t *testing.T) {
	medianoche := time.Date(2026, 7, 20, 0, 15, 0, 0, Location())
	got := IssueTime(medianoche)
	if got != "00:15:00" {
		t.Errorf("IssueTime = %q, want 00:15:00 (formato 24h, no 12h)", got)
	}
}
