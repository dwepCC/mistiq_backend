package service

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tukifac/pkg/facturador"
)

// Prueba de contrato contra el facturador REAL (solo lectura, sin red, sin SUNAT): carga el payload
// que produce el backend con la misma lógica de parseo del facturador (JMS + config/serializer de
// facturador_lycet + plantillas XML de Greenter) y comprueba que lo acepta y que los días del XML
// coinciden con la entrada. Se omite si no hay `php` o falta ../facturador_lycet/vendor.
func TestContract_FacturadorAcceptsRetentionPerceptionPayloads(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php no disponible: prueba de contrato PHP omitida")
	}
	root, _ := filepath.Abs(filepath.Join("..", "..", "..", "..", "facturador_lycet"))
	if _, err := os.Stat(filepath.Join(root, "vendor", "autoload.php")); err != nil {
		t.Skip("facturador_lycet/vendor no disponible: prueba de contrato PHP omitida")
	}
	script, _ := filepath.Abs(filepath.Join("testdata", "verify_facturador_dates.php"))

	stubExchangeRate(t)
	s := &BillingService{}
	rdet, err := s.buildRetentionDetails([]retentionDetailBuildInput{
		{TipoDoc: "01", NumDoc: "F001-1", FechaEmision: "2026-09-04", ImpTotal: 1000, Moneda: "USD",
			Pagos: []retentionPaymentInput{{Moneda: "USD", Importe: 1000, Fecha: "2026-09-05"}}, FechaRetencion: "2026-09-05", ImpRetenido: 30, ImpPagar: 970},
		{TipoDoc: "01", NumDoc: "F001-2", FechaEmision: "2026-09-05T04:59:00Z", ImpTotal: 500, Moneda: "PEN",
			Pagos: []retentionPaymentInput{{Moneda: "PEN", Importe: 500}}, ImpRetenido: 15, ImpPagar: 485},
	}, "2026-09-05T09:00:00-05:00")
	if err != nil {
		t.Fatal(err)
	}
	pdet, err := s.buildPerceptionDetails([]perceptionDetailBuildInput{
		{TipoDoc: "01", NumDoc: "F001-9", FechaEmision: "2026-12-31", ImpTotal: 2000, Moneda: "USD",
			Cobros: []retentionPaymentInput{{Moneda: "USD", Importe: 2000, Fecha: "2026-12-31"}}, FechaPercepcion: "2027-01-01", ImpPercibido: 40, ImpCobrar: 1960},
	}, "2027-01-01T08:00:00-05:00")
	if err != nil {
		t.Fatal(err)
	}
	co := facturador.InvoiceCompany{RUC: "20123456789", RazonSocial: "EMPRESA SAC"}
	cl := facturador.InvoiceClient{TipoDoc: "6", NumDoc: "20601030013", RznSocial: "PROVEEDOR SAC"}
	rp := &facturador.RetentionPayload{Serie: "R001", Correlativo: "1", FechaEmision: "2026-09-05T09:00:00-05:00", Company: co, Proveedor: cl, Regimen: "01", Tasa: 3, ImpRetenido: 45, ImpPagado: 1455, Details: rdet}
	pp := &facturador.PerceptionPayload{Serie: "P001", Correlativo: "1", FechaEmision: "2027-01-01T08:00:00-05:00", Company: co, Proveedor: cl, Regimen: "01", Tasa: 2, ImpPercibido: 40, ImpCobrado: 1960, Details: pdet}

	run := func(kind string, payload interface{}) string {
		b, _ := json.Marshal(payload)
		f := filepath.Join(t.TempDir(), kind+".json")
		if err := os.WriteFile(f, b, 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(php, script, root, kind, f).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: el facturador rechazaría el payload: %v\n%s", kind, err, out)
		}
		return string(out)
	}
	rout := run("retention", rp)
	for _, want := range []string{
		"cbc:IssueDate = 2026-09-05", "cbc:IssueDate = 2026-09-04", "cbc:PaidDate = 2026-09-05",
		"sac:SUNATRetentionDate = 2026-09-05", "cbc:Date = 2026-09-04", // TC de la 1.ª línea; 2.º detalle (04:59Z) sigue siendo 4-sep en Lima
	} {
		if !strings.Contains(rout, want) {
			t.Errorf("retención: falta %q en el XML:\n%s", want, rout)
		}
	}
	pout := run("perception", pp)
	for _, want := range []string{
		"cbc:IssueDate = 2027-01-01", "cbc:IssueDate = 2026-12-31", "cbc:PaidDate = 2026-12-31",
		"sac:SUNATPerceptionDate = 2027-01-01", "cbc:Date = 2026-12-31",
	} {
		if !strings.Contains(pout, want) {
			t.Errorf("percepción: falta %q en el XML:\n%s", want, pout)
		}
	}

	// Control negativo: el payload "solo día" (el del incidente) SÍ es rechazado por el facturador.
	legacy := `{"serie":"R001","correlativo":"1","fechaEmision":"2026-09-05T09:00:00-05:00","details":[{"tipoDoc":"01","numDoc":"F001-1","fechaEmision":"2026-09-04","impTotal":1}]}`
	f := filepath.Join(t.TempDir(), "legacy.json")
	_ = os.WriteFile(f, []byte(legacy), 0o644)
	out, err := exec.Command(php, script, root, "retention", f).CombinedOutput()
	if err == nil || !strings.Contains(string(out), `Invalid datetime "2026-09-04"`) {
		t.Errorf("el control negativo debía reproducir el error de producción; err=%v out=%s", err, out)
	}
}
