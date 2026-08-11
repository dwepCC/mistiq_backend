package docusage

import (
	"fmt"
	"time"
)

const limaTZ = "America/Lima"

var limaLoc *time.Location

func lima() *time.Location {
	if limaLoc != nil {
		return limaLoc
	}
	loc, err := time.LoadLocation(limaTZ)
	if err != nil {
		limaLoc = time.FixedZone("America/Lima", -5*3600)
	} else {
		limaLoc = loc
	}
	return limaLoc
}

func nowLima() time.Time {
	return time.Now().In(lima())
}

func calendarDateLima(t time.Time) time.Time {
	lt := t.In(lima())
	return time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, lima())
}

var mesesES = [...]string{
	"enero", "febrero", "marzo", "abril", "mayo", "junio",
	"julio", "agosto", "setiembre", "octubre", "noviembre", "diciembre",
}

// formatDayMonth convierte "2026-09-03" en "3 de setiembre", para los avisos al usuario.
// Si la fecha no es parseable devuelve el valor tal cual: un mensaje con la fecha cruda
// es mejor que uno vacío.
func formatDayMonth(ymd string) string {
	t, err := time.ParseInLocation("2006-01-02", ymd, lima())
	if err != nil {
		return ymd
	}
	return fmt.Sprintf("%d de %s", t.Day(), mesesES[int(t.Month())-1])
}
