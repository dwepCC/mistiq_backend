package service

import (
	"encoding/json"
	"regexp"
	"testing"

	"tukifac/pkg/facturador"
)

// Contrato del facturador (JMS DateHandler, type: DateTime sin formato => Y-m-d\TH:i:sP),
// siempre con el offset de Lima.
var fiscalDateContractRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}-05:00$`)

// dateFieldsRe localiza en el JSON serializado cualquier campo cuyo nombre empiece por "fecha".
var dateFieldsRe = regexp.MustCompile(`"(fecha\w*)":"([^"]*)"`)

// assertAllDatesMatchContract serializa el payload igual que pkg/facturador/client.go
// (json.Marshal) y exige que TODA fecha cumpla el formato. Devuelve los valores por campo.
func assertAllDatesMatchContract(t *testing.T, payload interface{}) map[string][]string {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string][]string{}
	for _, m := range dateFieldsRe.FindAllStringSubmatch(string(b), -1) {
		found[m[1]] = append(found[m[1]], m[2])
		if !fiscalDateContractRe.MatchString(m[2]) {
			t.Errorf("campo %q = %q no cumple Y-m-d\\TH:i:sP con -05:00 (el facturador respondería: Invalid datetime %q)", m[1], m[2], m[2])
		}
	}
	return found
}

func day(s string) string { return s[:10] }

// Regresión del incidente: las 5 retenciones de producción fallaron con
// `Invalid datetime "AAAA-MM-DD", expected one of the format "Y-m-d\TH:i:sP"` porque
// buildRetentionDetails enviaba fechas "solo día". Con el código anterior este test FALLA.
func TestRegression_RetentionDetailsDatesMatchFacturadorContract(t *testing.T) {
	s := &BillingService{}
	details, err := s.buildRetentionDetails([]retentionDetailBuildInput{
		{
			TipoDoc: "01", NumDoc: "F001-10", FechaEmision: "2026-09-01T00:00:00-05:00", ImpTotal: 1000, Moneda: "PEN",
			Pagos:          []retentionPaymentInput{{Moneda: "PEN", Importe: 600, Fecha: "2026-09-02"}, {Moneda: "PEN", Importe: 400, Fecha: "2026-09-03"}},
			FechaRetencion: "2026-09-04T10:15:00-05:00", ImpRetenido: 30, ImpPagar: 970,
		},
		{
			TipoDoc: "01", NumDoc: "F001-11", FechaEmision: "2026-08-31", ImpTotal: 500, Moneda: "PEN",
			Pagos: []retentionPaymentInput{{Moneda: "PEN", Importe: 500}}, ImpRetenido: 15, ImpPagar: 485,
		},
	}, "2026-09-04T10:15:00-05:00")
	if err != nil {
		t.Fatal(err)
	}
	payload := &facturador.RetentionPayload{FechaEmision: "2026-09-04T10:15:00-05:00", Regimen: "01", Tasa: 3, ImpRetenido: 45, ImpPagado: 1455, Details: details}
	found := assertAllDatesMatchContract(t, payload)

	// Los valores de día coinciden con la entrada (sin desplazamientos).
	wantDays := map[string][]string{
		"fechaEmision":   {"2026-09-04", "2026-09-01", "2026-08-31"},
		"fechaRetencion": {"2026-09-04", "2026-09-04"}, // el 2.º hereda la del documento
		"fecha":          {"2026-09-02", "2026-09-03", "2026-09-04"},
	}
	for k, want := range wantDays {
		got := found[k]
		if len(got) != len(want) {
			t.Fatalf("%s: %d valores (%v), esperados %d", k, len(got), got, len(want))
		}
		for i := range want {
			if day(got[i]) != want[i] {
				t.Errorf("%s[%d] = %q, el día esperado era %s", k, i, got[i], want[i])
			}
		}
	}
}

func TestRegression_PerceptionDetailsDatesMatchFacturadorContract(t *testing.T) {
	s := &BillingService{}
	details, err := s.buildPerceptionDetails([]perceptionDetailBuildInput{
		{
			TipoDoc: "01", NumDoc: "F001-20", FechaEmision: "2026-09-01", ImpTotal: 1000, Moneda: "PEN",
			Cobros:          []retentionPaymentInput{{Moneda: "PEN", Importe: 1000, Fecha: "2026-09-02"}},
			FechaPercepcion: "2026-09-04", ImpPercibido: 20, ImpCobrar: 980,
		},
		{
			TipoDoc: "03", NumDoc: "B001-5", FechaEmision: "2026-09-05T04:59:00Z", ImpTotal: 200, Moneda: "PEN",
			Cobros: []retentionPaymentInput{{Moneda: "PEN", Importe: 200}}, ImpPercibido: 2, ImpCobrar: 198,
		},
	}, "2026-09-04T10:15:00-05:00")
	if err != nil {
		t.Fatal(err)
	}
	payload := &facturador.PerceptionPayload{FechaEmision: "2026-09-04T10:15:00-05:00", Regimen: "01", Tasa: 2, ImpPercibido: 22, ImpCobrado: 1178, Details: details}
	found := assertAllDatesMatchContract(t, payload)
	// 04:59Z del 5-sep es el 4-sep a las 23:59 en Lima.
	if got := found["fechaEmision"]; len(got) != 3 || day(got[2]) != "2026-09-04" {
		t.Errorf("fechaEmision del 2.º detalle debía ser el día 2026-09-04 en Lima: %v", got)
	}
	if got := found["fechaPercepcion"]; len(got) != 2 || day(got[0]) != "2026-09-04" {
		t.Errorf("fechaPercepcion: %v", got)
	}
}

func TestBuildRetentionDetails_invalidDateIsClearError(t *testing.T) {
	s := &BillingService{}
	_, err := s.buildRetentionDetails([]retentionDetailBuildInput{{
		TipoDoc: "01", NumDoc: "F001-1", FechaEmision: "no-es-fecha", ImpTotal: 100, Moneda: "PEN",
		Pagos: []retentionPaymentInput{{Moneda: "PEN", Importe: 100}}, ImpRetenido: 3, ImpPagar: 97,
	}}, "2026-09-04T10:15:00-05:00")
	if err == nil {
		t.Fatal("una fecha basura debe rechazarse antes de llegar al facturador")
	}
}

// Los payloads persistidos antes del fix (fechas solo día) deben salir correctos al reenviar,
// sin modificar lo guardado.
func TestEnrichFiscalPayloadJSON_normalizesLegacyRetentionDates(t *testing.T) {
	legacy := `{"fechaEmision":"2026-09-04T10:15:00-05:00","details":[{"fechaEmision":"2026-09-01","fechaRetencion":"2026-09-04","pagos":[{"fecha":"2026-09-02"}],"tipoCambio":{"fecha":"2026-09-01"}}]}`
	out := enrichFiscalPayloadJSON(legacy, "20", "retention")
	n := 0
	for _, m := range dateFieldsRe.FindAllStringSubmatch(out, -1) {
		n++
		if !fiscalDateContractRe.MatchString(m[2]) {
			t.Errorf("%s = %q sin normalizar", m[1], m[2])
		}
	}
	if n != 5 {
		t.Errorf("se esperaban 5 fechas, hay %d: %s", n, out)
	}
	// El resto de documentos no se toca.
	inv := `{"fechaEmision":"2026-09-04"}`
	if got := enrichFiscalPayloadJSON(inv, "01", "invoice"); !regexp.MustCompile(`"fechaEmision":"2026-09-04"`).MatchString(got) {
		t.Errorf("facturas no deben alterarse: %s", got)
	}
}
