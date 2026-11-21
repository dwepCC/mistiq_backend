package service

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/datespe"
)

func TestResolveCreditNoteIssueDate(t *testing.T) {
	loc := datespe.Location()
	now := time.Date(2026, 10, 1, 15, 30, 0, 0, loc)

	t.Run("vacío usa la hora actual", func(t *testing.T) {
		got, err := ResolveCreditNoteIssueDate("", now)
		if err != nil || !got.Equal(now) {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("hoy conserva la hora real", func(t *testing.T) {
		got, err := ResolveCreditNoteIssueDate("2026-10-01", now)
		if err != nil || !got.Equal(now) {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("hasta 3 días atrás se acepta y queda al mediodía de Lima", func(t *testing.T) {
		got, err := ResolveCreditNoteIssueDate("2026-09-28", now)
		if err != nil {
			t.Fatal(err)
		}
		l := got.In(loc)
		if l.Day() != 28 || l.Hour() != 12 {
			t.Errorf("esperaba 28/09 12:00, got %v", l)
		}
	})
	t.Run("4 días atrás se rechaza", func(t *testing.T) {
		_, err := ResolveCreditNoteIssueDate("2026-09-27", now)
		if err == nil || !strings.Contains(err.Error(), "anterior a 3 días") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("fecha futura se rechaza", func(t *testing.T) {
		_, err := ResolveCreditNoteIssueDate("2026-10-02", now)
		if err == nil || !strings.Contains(err.Error(), "futura") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("un comprobante de hoy admite fechas de los 3 días anteriores", func(t *testing.T) {
		// La ventana no depende de la fecha del comprobante: con hoy = 01/10 se admite 28, 29 y 30/09.
		for _, d := range []string{"2026-09-28", "2026-09-29", "2026-09-30", "2026-10-01"} {
			if _, err := ResolveCreditNoteIssueDate(d, now); err != nil {
				t.Errorf("%s debe aceptarse: %v", d, err)
			}
		}
	})
	t.Run("formato inválido", func(t *testing.T) {
		if _, err := ResolveCreditNoteIssueDate("01/10/2026", now); err == nil {
			t.Fatal("esperaba error de formato")
		}
	})
	t.Run("el límite usa el calendario de Perú, no UTC", func(t *testing.T) {
		// 02:00 UTC del 02/10 = 21:00 del 01/10 en Lima: "hoy" sigue siendo el 01/10.
		utc := time.Date(2026, 10, 2, 2, 0, 0, 0, time.UTC)
		if _, err := ResolveCreditNoteIssueDate("2026-10-01", utc); err != nil {
			t.Fatalf("hoy en Lima debe ser válido: %v", err)
		}
		if _, err := ResolveCreditNoteIssueDate("2026-10-02", utc); err == nil {
			t.Fatal("el 02/10 todavía es futuro en Lima")
		}
	})
}
