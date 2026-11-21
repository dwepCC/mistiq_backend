package service

import (
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/datespe"
)

// CreditNoteMaxBackdateDays: cuántos días hacia atrás se permite fechar una nota de crédito.
const CreditNoteMaxBackdateDays = 3

// ResolveCreditNoteIssueDate valida y resuelve la fecha de emisión de una nota de crédito.
//
//   - raw vacío: hoy, con la hora real (comportamiento de siempre).
//   - raw "YYYY-MM-DD": no puede ser futura ni tener más de CreditNoteMaxBackdateDays días de
//     antigüedad (calendario de Perú). La ventana es siempre "hoy − 3 días … hoy", sin importar la
//     fecha del comprobante que corrige (con hoy = 01/10 se admite hasta el 28/09).
//
// Una fecha distinta de hoy se guarda a las 12:00 de Lima: el payload fiscal usa solo la fecha
// calendario (facturador.FormatFiscalDateTime la normaliza al mediodía), y así un cambio de zona
// horaria del servidor no mueve la nota de día.
func ResolveCreditNoteIssueDate(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return now, nil
	}
	loc := datespe.Location()
	day, err := time.ParseInLocation("2006-01-02", raw, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("fecha de emisión inválida %q (use AAAA-MM-DD)", raw)
	}
	nowLima := now.In(loc)
	today := time.Date(nowLima.Year(), nowLima.Month(), nowLima.Day(), 0, 0, 0, 0, loc)
	if day.After(today) {
		return time.Time{}, fmt.Errorf("la fecha de emisión de la nota no puede ser futura")
	}
	oldest := today.AddDate(0, 0, -CreditNoteMaxBackdateDays)
	if day.Before(oldest) {
		return time.Time{}, fmt.Errorf("la fecha de emisión de la nota no puede ser anterior a %d días (mínimo %s)",
			CreditNoteMaxBackdateDays, oldest.Format("02/01/2006"))
	}
	if day.Equal(today) {
		return now, nil
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, loc), nil
}
