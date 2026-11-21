package facturador

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// FiscalDateTimeLayout es el formato exigido por facturador_lycet / JMS (Y-m-d\TH:i:sP).
const FiscalDateTimeLayout = "2006-01-02T15:04:05-07:00"

func limaLocation() *time.Location {
	loc, err := time.LoadLocation("America/Lima")
	if err != nil {
		return time.FixedZone("America/Lima", -5*3600)
	}
	return loc
}

// FormatFiscalDateTime formatea una fecha/hora para payloads SUNAT/PSE (fecha calendario Perú al mediodía).
func FormatFiscalDateTime(t time.Time) string {
	loc := limaLocation()
	d := t.In(loc)
	normalized := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, loc)
	return normalized.Format(FiscalDateTimeLayout)
}

// FormatFiscalDateTimeExact formatea con la hora real (sin normalizar a mediodía). A diferencia
// de facturas/boletas —donde SUNAT solo valida la fecha calendario, por eso FormatFiscalDateTime
// fija 12:00:00 y evita corrimientos de día por zona horaria— el servicio GRE de guía de
// remisión valida fechaEmision con precisión de hora contra el momento real de recepción en
// SUNAT. Usar FormatFiscalDateTime ahí hacía que una guía emitida antes del mediodía llegara con
// una hora "futura" respecto al envío real, y SUNAT la rechazaba con 2108 "Presentación fuera de
// fecha o con Fecha/hora mayor a la recepción en SUNAT" — bug real en producción (dos guías
// rechazadas, tenant RUC 20607277193, ambas emitidas ~08:3x con fechaEmision en 12:00:00).
func FormatFiscalDateTimeExact(t time.Time) string {
	return t.In(limaLocation()).Format(FiscalDateTimeLayout)
}

// NormalizeFiscalDateTimeString convierte fechas sin zona horaria al layout exigido por JMS (Y-m-d\TH:i:sP).
func NormalizeFiscalDateTimeString(value, fallback string) string {
	value = strings.TrimSpace(value)
	fallback = strings.TrimSpace(fallback)
	if value == "" {
		return NormalizeFiscalDateTimeString(fallback, time.Now().In(limaLocation()).Format(FiscalDateTimeLayout))
	}
	if t, err := time.Parse(FiscalDateTimeLayout, value); err == nil {
		return t.Format(FiscalDateTimeLayout)
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.In(limaLocation()).Format(FiscalDateTimeLayout)
	}
	loc := limaLocation()
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			return t.Format(FiscalDateTimeLayout)
		}
	}
	if fallback != "" {
		return NormalizeFiscalDateTimeString(fallback, "")
	}
	return value
}

// ErrFiscalDateInvalid indica que un valor de fecha no se pudo interpretar.
var ErrFiscalDateInvalid = errors.New("fecha inválida")

// parseFiscalInstant interpreta una fecha que viene del cliente/BD y devuelve el instante y si el
// valor era "solo día" (sin hora).
//
//   - RFC3339 con zona ("2026-09-04T10:00:00-05:00", "...Z"): conserva el instante.
//   - Sin zona ("2026-09-04T10:00:00", "2026-09-04 10:00:00", "2026-09-04T10:00"): hora de Lima.
//   - Solo día ("2026-09-04"): ese día calendario de Lima, sin desplazarlo.
func parseFiscalInstant(value string) (t time.Time, dayOnly bool, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false, ErrFiscalDateInvalid
	}
	if t, e := time.Parse(time.RFC3339, value); e == nil {
		return t, false, nil
	}
	loc := limaLocation()
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04"} {
		if t, e := time.ParseInLocation(layout, value, loc); e == nil {
			return t, false, nil
		}
	}
	if t, e := time.ParseInLocation("2006-01-02", value, loc); e == nil {
		return t, true, nil
	}
	return time.Time{}, false, fmt.Errorf("%w: %q (use AAAA-MM-DD o AAAA-MM-DDTHH:MM:SS-05:00)", ErrFiscalDateInvalid, value)
}

// FiscalDateTimeFromInput convierte la fecha de entrada al layout exigido por el facturador
// (RFC3339 con offset, Y-m-d\TH:i:sP) en hora de Perú. Un "solo día" se fija al mediodía de ese
// mismo día en Lima (SUNAT solo usa la parte de fecha; el mediodía evita corrimientos de día).
// Es idempotente: una cadena ya normalizada se devuelve igual.
func FiscalDateTimeFromInput(value string) (string, error) {
	t, dayOnly, err := parseFiscalInstant(value)
	if err != nil {
		return "", err
	}
	if dayOnly {
		return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, limaLocation()).Format(FiscalDateTimeLayout), nil
	}
	return t.In(limaLocation()).Format(FiscalDateTimeLayout), nil
}

// FiscalCalendarDay devuelve el día calendario (AAAA-MM-DD) de Lima de la fecha de entrada.
// Sirve para consultas por día (p. ej. tipo de cambio).
func FiscalCalendarDay(value string) (string, error) {
	t, dayOnly, err := parseFiscalInstant(value)
	if err != nil {
		return "", err
	}
	if dayOnly {
		return t.Format("2006-01-02"), nil
	}
	return t.In(limaLocation()).Format("2006-01-02"), nil
}

// NormalizeRetentionPerceptionPayloadDates lleva TODAS las fechas de un payload de retención o
// percepción (en forma de mapa JSON) al layout del facturador. Se aplica al enviar, de modo que
// también cubre payloads ya persistidos con fechas "solo día" sin modificarlos en BD.
// Un valor ausente o inválido se deja intacto (el facturador reportará el error real).
func NormalizeRetentionPerceptionPayloadDates(m map[string]interface{}) {
	if m == nil {
		return
	}
	fixStr := func(obj map[string]interface{}, key string) {
		if s, ok := obj[key].(string); ok && strings.TrimSpace(s) != "" {
			if n, err := FiscalDateTimeFromInput(s); err == nil {
				obj[key] = n
			}
		}
	}
	fixStr(m, "fechaEmision")
	details, _ := m["details"].([]interface{})
	for _, d := range details {
		det, ok := d.(map[string]interface{})
		if !ok {
			continue
		}
		fixStr(det, "fechaEmision")
		fixStr(det, "fechaRetencion")
		fixStr(det, "fechaPercepcion")
		for _, key := range []string{"pagos", "cobros"} {
			lines, _ := det[key].([]interface{})
			for _, l := range lines {
				if line, ok := l.(map[string]interface{}); ok {
					fixStr(line, "fecha")
				}
			}
		}
		if tc, ok := det["tipoCambio"].(map[string]interface{}); ok {
			fixStr(tc, "fecha")
		}
	}
}

// normalizeRetentionPayloadDates / normalizePerceptionPayloadDates aplican lo mismo sobre los
// structs tipados justo antes de serializar hacia /retention|/perception.
func normalizeRetentionPayloadDates(p *RetentionPayload) {
	if p == nil {
		return
	}
	norm := func(s *string) {
		if n, err := FiscalDateTimeFromInput(*s); err == nil {
			*s = n
		}
	}
	norm(&p.FechaEmision)
	for i := range p.Details {
		d := &p.Details[i]
		norm(&d.FechaEmision)
		norm(&d.FechaRetencion)
		for j := range d.Pagos {
			norm(&d.Pagos[j].Fecha)
		}
		if d.TipoCambio != nil {
			norm(&d.TipoCambio.Fecha)
		}
	}
}

func normalizePerceptionPayloadDates(p *PerceptionPayload) {
	if p == nil {
		return
	}
	norm := func(s *string) {
		if n, err := FiscalDateTimeFromInput(*s); err == nil {
			*s = n
		}
	}
	norm(&p.FechaEmision)
	for i := range p.Details {
		d := &p.Details[i]
		norm(&d.FechaEmision)
		norm(&d.FechaPercepcion)
		for j := range d.Cobros {
			norm(&d.Cobros[j].Fecha)
		}
		if d.TipoCambio != nil {
			norm(&d.TipoCambio.Fecha)
		}
	}
}
