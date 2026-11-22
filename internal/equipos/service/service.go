// Package service implementa el módulo «Gestión de Equipos» del panel central (exclusivo del dueño):
// catálogo, combos, transportistas, stock (kardex) e importación del Excel de control.
package service

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Service opera sobre la BD central.
type Service struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Service { return &Service{db: db} }

// ValidationError error de datos del usuario (el handler lo responde como 400).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// IsValidation indica si el error es de validación (400) y no un fallo interno.
func IsValidation(err error) bool {
	_, ok := err.(*ValidationError)
	return ok
}

// limaLoc zona horaria de negocio (las fechas «de calendario» se interpretan en Lima).
func limaLoc() *time.Location {
	if loc, err := time.LoadLocation("America/Lima"); err == nil {
		return loc
	}
	return time.FixedZone("America/Lima", -5*3600)
}

// periodBounds devuelve [inicio, fin) del período YYYY-MM en Lima.
func periodBounds(period string) (time.Time, time.Time, error) {
	period = strings.TrimSpace(period)
	t, err := time.ParseInLocation("2006-01", period, limaLoc())
	if err != nil {
		return time.Time{}, time.Time{}, invalid("período inválido (use AAAA-MM)")
	}
	return t, t.AddDate(0, 1, 0), nil
}

// currentPeriod mes en curso (YYYY-MM) en Lima.
func currentPeriod() string { return time.Now().In(limaLoc()).Format("2006-01") }

// noonLima mediodía de la fecha de calendario `d` en Lima (evita cambios de día por zona horaria).
func noonLima(d time.Time) time.Time {
	y, m, day := d.Date()
	return time.Date(y, m, day, 12, 0, 0, 0, limaLoc())
}

// CurrentPeriod mes en curso (YYYY-MM) en Lima.
func CurrentPeriod() string { return currentPeriod() }
