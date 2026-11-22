package service

import (
	"fmt"
	"time"
)

// Plazo de retorno de un paquete en agencia y umbrales del semáforo de la pestaña «Pedido-agencia».
// Son los únicos valores a tocar si el negocio cambia la regla (días calendario, hora de Lima).
const (
	AgencyReturnDays    = 15 // al llegar a este día en agencia corresponde el retorno
	AgencyGreenMaxDays  = 7  // 0–7 verde
	AgencyYellowMaxDays = 11 // 8–11 amarillo
	AgencyRedMaxDays    = 14 // 12–14 rojo; ≥ AgencyReturnDays crítico
)

// daysInAgency días calendario (hora de Lima) entre la fecha de llegada y hoy; nunca negativo.
func daysInAgency(arrived, today time.Time) int {
	a, t := arrived.In(limaLoc()), today.In(limaLoc())
	ad := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	td := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	if d := int(td.Sub(ad).Hours() / 24); d > 0 {
		return d
	}
	return 0
}

// agencyReturnStatus semáforo, texto y días restantes para el retorno según los días en agencia.
func agencyReturnStatus(days int) (level, label string, left int) {
	left = AgencyReturnDays - days
	switch {
	case days >= AgencyReturnDays:
		return "critico", "Retornar ya", 0
	case days > AgencyYellowMaxDays:
		level = "rojo"
	case days > AgencyGreenMaxDays:
		level = "amarillo"
	default:
		level = "verde"
	}
	if left == 1 {
		return level, "Falta 1 día", left
	}
	return level, fmt.Sprintf("Faltan %d días", left), left
}

// arrivalDate fecha de llegada a la agencia: hoy (Lima) por defecto; nunca futura.
func arrivalDate(raw string) (time.Time, error) {
	d, err := parseDateOnly(raw)
	if err != nil {
		return time.Time{}, err
	}
	today := todayNoon()
	if d == nil {
		return today, nil
	}
	if d.After(today) {
		return time.Time{}, invalid("la fecha de llegada no puede ser futura")
	}
	return *d, nil
}

// agencyExpired indica si el paquete ya cumplió el plazo único de retorno (mismo día que el semáforo «crítico»).
func agencyExpired(daysSinceArrival *int) bool {
	return daysSinceArrival != nil && *daysSinceArrival >= AgencyReturnDays
}
