package facturador

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

// Formato exigido por JMS en facturador_lycet (DateHandler: Y-m-d\TH:i:sP) con offset de Lima.
var fiscalDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}-05:00$`)

func TestFiscalDateTimeFromInput(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"solo día", "2026-09-04", "2026-09-04T12:00:00-05:00"},
		{"rfc3339 Lima", "2026-09-04T10:30:00-05:00", "2026-09-04T10:30:00-05:00"},
		{"rfc3339 Lima medianoche (frontend)", "2026-09-04T00:00:00-05:00", "2026-09-04T00:00:00-05:00"},
		{"UTC 23:30 = mismo día en Lima", "2026-09-04T23:30:00Z", "2026-09-04T18:30:00-05:00"},
		{"UTC 04:59 = día anterior en Lima", "2026-09-05T04:59:00Z", "2026-09-04T23:59:00-05:00"},
		{"UTC 05:00 = mismo día en Lima", "2026-09-05T05:00:00Z", "2026-09-05T00:00:00-05:00"},
		{"offset +02:00", "2026-09-04T01:00:00+02:00", "2026-09-03T18:00:00-05:00"},
		{"sin zona = hora Lima", "2026-09-04T10:30:00", "2026-09-04T10:30:00-05:00"},
		{"espacio sin zona", "2026-09-04 10:30:00", "2026-09-04T10:30:00-05:00"},
		{"datetime-local sin segundos", "2026-09-04T10:30", "2026-09-04T10:30:00-05:00"},
		{"con espacios", "  2026-09-04  ", "2026-09-04T12:00:00-05:00"},
		{"fin de año", "2026-12-31", "2026-12-31T12:00:00-05:00"},
		{"inicio de año", "2027-01-01", "2027-01-01T12:00:00-05:00"},
		{"fin de mes", "2026-02-28", "2026-02-28T12:00:00-05:00"},
		{"bisiesto", "2028-02-29", "2028-02-29T12:00:00-05:00"},
		{"cruce de año por zona", "2027-01-01T04:30:00Z", "2026-12-31T23:30:00-05:00"},
		{"fracciones", "2026-09-04T10:30:00.123Z", "2026-09-04T05:30:00-05:00"},
	}
	for _, c := range cases {
		got, err := FiscalDateTimeFromInput(c.in)
		if err != nil {
			t.Fatalf("%s: error inesperado: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: entrada %q -> %q, esperado %q", c.name, c.in, got, c.want)
		}
		if !fiscalDateRe.MatchString(got) {
			t.Errorf("%s: %q no cumple el formato del facturador", c.name, got)
		}
		// Idempotencia: normalizar lo ya normalizado no cambia nada.
		again, err := FiscalDateTimeFromInput(got)
		if err != nil || again != got {
			t.Errorf("%s: no idempotente: %q -> %q (%v)", c.name, got, again, err)
		}
	}
}

func TestFiscalDateTimeFromInput_invalid(t *testing.T) {
	for _, in := range []string{"", "   ", "basura", "04/09/2026", "2026-13-01", "2026-02-30", "2027-02-29", "2026-09-04T25:00:00", "2026-09-04abc", "20260904"} {
		if got, err := FiscalDateTimeFromInput(in); err == nil {
			t.Errorf("%q debía fallar y devolvió %q", in, got)
		}
	}
}

func TestFiscalCalendarDay(t *testing.T) {
	cases := map[string]string{
		"2026-09-04":                "2026-09-04",
		"2026-09-04T12:00:00-05:00": "2026-09-04",
		"2026-09-04T23:30:00Z":      "2026-09-04",
		"2026-09-05T04:59:00Z":      "2026-09-04",
		"2026-09-05T05:00:00Z":      "2026-09-05",
		"2026-12-31":                "2026-12-31",
		"2027-01-01T04:59:59Z":      "2026-12-31",
	}
	for in, want := range cases {
		got, err := FiscalCalendarDay(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q (%v), esperado %q", in, got, err, want)
		}
	}
	if _, err := FiscalCalendarDay("basura"); err == nil {
		t.Error("basura debía fallar")
	}
}

// Un payload ya persistido (fechas "solo día", como los 5 rechazados) se normaliza al enviar
// sin tocar lo guardado: el mapa de entrada se transforma en memoria.
func TestNormalizeRetentionPerceptionPayloadDates_legacyMap(t *testing.T) {
	raw := `{"fechaEmision":"2026-09-04T10:00:00-05:00","details":[{"fechaEmision":"2026-09-01","fechaRetencion":"2026-09-04",
	"pagos":[{"fecha":"2026-09-02","importe":10}],"cobros":[{"fecha":"2026-09-03"}],"tipoCambio":{"fecha":"2026-09-01","factor":3.7}}]}`
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	NormalizeRetentionPerceptionPayloadDates(m)
	b, _ := json.Marshal(m)
	var out struct {
		FechaEmision string `json:"fechaEmision"`
		Details      []struct {
			FechaEmision   string                   `json:"fechaEmision"`
			FechaRetencion string                   `json:"fechaRetencion"`
			Pagos          []map[string]interface{} `json:"pagos"`
			Cobros         []map[string]interface{} `json:"cobros"`
			TipoCambio     map[string]interface{}   `json:"tipoCambio"`
		} `json:"details"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	d := out.Details[0]
	for name, v := range map[string]string{
		"fechaEmision": out.FechaEmision, "det.fechaEmision": d.FechaEmision, "det.fechaRetencion": d.FechaRetencion,
		"pagos[0].fecha": d.Pagos[0]["fecha"].(string), "cobros[0].fecha": d.Cobros[0]["fecha"].(string),
		"tipoCambio.fecha": d.TipoCambio["fecha"].(string),
	} {
		if !fiscalDateRe.MatchString(v) {
			t.Errorf("%s = %q no cumple el formato", name, v)
		}
	}
	if d.FechaEmision != "2026-09-01T12:00:00-05:00" || d.Pagos[0]["importe"].(float64) != 10 {
		t.Errorf("valores alterados: %+v", d)
	}
	NormalizeRetentionPerceptionPayloadDates(nil) // no debe entrar en pánico
}

// El cliente normaliza justo antes de serializar: aunque llegue un payload viejo con fechas "solo
// día", lo que sale hacia /retention|/perception cumple el contrato. Servidor HTTP local de prueba.
func TestClientRetentionPerception_bodyDatesNormalized(t *testing.T) {
	var gotPaths []string
	var gotBodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPaths = append(gotPaths, r.URL.Path)
		gotBodies = append(gotBodies, b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "x")

	ret := &RetentionPayload{FechaEmision: "2026-09-04", Details: []RetentionDetail{{
		FechaEmision: "2026-09-01", FechaRetencion: "2026-09-04",
		Pagos:      []RetentionPayment{{Fecha: "2026-09-02"}},
		TipoCambio: &RetentionExchange{Fecha: "2026-09-01"},
	}}}
	per := &PerceptionPayload{FechaEmision: "2026-09-04", Details: []PerceptionDetail{{
		FechaEmision: "2026-09-01", FechaPercepcion: "2026-09-04",
		Cobros:     []RetentionPayment{{Fecha: "2026-09-02"}},
		TipoCambio: &RetentionExchange{Fecha: "2026-09-01"},
	}}}
	_, _ = c.SendRetention(ret)
	_, _ = c.GetRetentionXML(ret)
	_, _ = c.GetRetentionPDF(ret)
	_, _ = c.SendPerception(per)
	_, _ = c.GetPerceptionXML(per)
	_, _ = c.GetPerceptionPDF(per)
	if len(gotBodies) != 6 {
		t.Fatalf("se esperaban 6 llamadas, hubo %d", len(gotBodies))
	}
	for i, body := range gotBodies {
		for _, m := range regexp.MustCompile(`"(fecha\w*)":"([^"]*)"`).FindAllStringSubmatch(string(body), -1) {
			if !fiscalDateRe.MatchString(m[2]) {
				t.Errorf("%s: campo %s = %q no cumple el formato", gotPaths[i], m[1], m[2])
			}
		}
	}
}
