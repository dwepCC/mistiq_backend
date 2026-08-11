package service

import (
	"strings"
	"testing"
	"time"

	"tukifac/config"
	"tukifac/pkg/saas"
)

func withBackdateDays(t *testing.T, days int) {
	t.Helper()
	prev := config.AppConfig
	config.AppConfig = &config.Config{SunatMaxBackdateDays: days}
	t.Cleanup(func() { config.AppConfig = prev })
}

// La fecha de reemisión se acota al plazo de envío de SUNAT: hoy y los días de
// atraso configurados entran; el día anterior al límite y cualquier fecha futura
// se rechazan antes de gastar un envío.
func TestValidateReissueDate_ventanaSunat(t *testing.T) {
	withBackdateDays(t, 3)
	today := saas.CalendarDateLima(saas.NowLima())

	cases := []struct {
		name    string
		offset  int
		wantErr bool
	}{
		{"hoy", 0, false},
		{"ayer", -1, false},
		{"limite de la ventana", -3, false},
		{"un dia fuera de la ventana", -4, true},
		{"manana", 1, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateReissueDate(today.AddDate(0, 0, tc.offset))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("offset %d debía rechazarse", tc.offset)
				}
				return
			}
			if err != nil {
				t.Fatalf("offset %d debía aceptarse: %v", tc.offset, err)
			}
			want := today.AddDate(0, 0, tc.offset)
			if got.Year() != want.Year() || got.Month() != want.Month() || got.Day() != want.Day() {
				t.Fatalf("día calendario alterado: got %s, want %s",
					got.Format("2006-01-02"), want.Format("2006-01-02"))
			}
		})
	}
}

// La hora se fija a mediodía para que un desfase de zona horaria no corra el
// comprobante al día anterior o siguiente.
func TestValidateReissueDate_normalizaAMediodia(t *testing.T) {
	withBackdateDays(t, 3)
	today := saas.CalendarDateLima(saas.NowLima())

	got, err := validateReissueDate(today.Add(23 * time.Hour))
	if err != nil {
		t.Fatalf("no debía fallar: %v", err)
	}
	if got.Hour() != 12 {
		t.Fatalf("hora esperada 12, got %d", got.Hour())
	}
}

func TestValidateReissueDate_fechaVacia(t *testing.T) {
	withBackdateDays(t, 3)
	if _, err := validateReissueDate(time.Time{}); err == nil {
		t.Fatal("una fecha vacía debe rechazarse")
	}
}

// Sin configuración cargada la validación no debe abrirse: cae al plazo por
// defecto de 3 días en lugar de aceptar cualquier fecha.
func TestValidateReissueDate_sinConfigUsaDefault(t *testing.T) {
	prev := config.AppConfig
	config.AppConfig = nil
	t.Cleanup(func() { config.AppConfig = prev })

	today := saas.CalendarDateLima(saas.NowLima())
	if _, err := validateReissueDate(today.AddDate(0, 0, -3)); err != nil {
		t.Fatalf("el límite por defecto debía aceptarse: %v", err)
	}
	if _, err := validateReissueDate(today.AddDate(0, 0, -4)); err == nil {
		t.Fatal("fuera del límite por defecto debía rechazarse")
	}
}

// classifyAfterSync corta el reenvío de un comprobante aceptado salvo cuando el
// origen es la corrección de soporte: ese es justo el caso que viene a resolver
// (aceptación obtenida contra SUNAT beta, inválida en producción).
func TestClassifyAfterSync_reissueIgnoraAceptacionPrevia(t *testing.T) {
	sync := SSOTSyncOutcome{ManualStatus: "already_accepted"}

	if got := classifyAfterSync(sync, true, FiscalSourceManualResend); got != "already_accepted" {
		t.Fatalf("reenvío normal debe cortar en already_accepted, got %q", got)
	}
	if got := classifyAfterSync(sync, true, FiscalSourceReissue); got != "allow" {
		t.Fatalf("la reemisión debe continuar, got %q", got)
	}
}

// El tope de la observación es el que exige SUNAT para cbc:Note; se mide en
// runes porque una tilde no debe contar doble.
func TestMaxFiscalObservation_seMideEnRunes(t *testing.T) {
	obs := strings.Repeat("á", maxFiscalObservationLen)
	if len([]rune(obs)) != maxFiscalObservationLen {
		t.Fatalf("la observación debe medirse en runes, got %d", len([]rune(obs)))
	}
	if len([]rune(obs+"á")) <= maxFiscalObservationLen {
		t.Fatal("un carácter de más debe superar el tope")
	}
}
