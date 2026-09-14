package facturador

import (
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
