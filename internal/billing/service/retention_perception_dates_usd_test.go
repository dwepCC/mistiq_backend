package service

import (
	"testing"

	"tukifac/internal/exchangerate"
	"tukifac/pkg/facturador"
)

func stubExchangeRate(t *testing.T) *[]string {
	t.Helper()
	var asked []string
	orig := exchangeRateLookup
	exchangeRateLookup = func(day string) (*exchangerate.QueryResult, error) {
		asked = append(asked, day)
		return &exchangerate.QueryResult{Success: true, Fecha: day, Venta: 3.75, Compra: 3.70}, nil
	}
	t.Cleanup(func() { exchangeRateLookup = orig })
	return &asked
}

// Retención y percepción completas en USD (tipo de cambio automático y manual) con varios
// detalles/pagos: todas las fechas cumplen el contrato y el tipo de cambio se consulta por el
// DÍA correcto de Lima.
func TestIntegration_USDRetentionAndPerceptionPayloads(t *testing.T) {
	asked := stubExchangeRate(t)
	s := &BillingService{}

	rdet, err := s.buildRetentionDetails([]retentionDetailBuildInput{
		{
			TipoDoc: "01", NumDoc: "F001-1", FechaEmision: "2026-09-04", ImpTotal: 1000, Moneda: "USD",
			Pagos:          []retentionPaymentInput{{Moneda: "USD", Importe: 1000, Fecha: "2026-09-05"}},
			FechaRetencion: "2026-09-05", ImpRetenido: 30, ImpPagar: 970,
		},
		{
			TipoDoc: "01", NumDoc: "F001-2", FechaEmision: "2026-09-05T04:59:00Z", ImpTotal: 500, Moneda: "USD",
			Pagos:       []retentionPaymentInput{{Moneda: "USD", Importe: 500}},
			ImpRetenido: 15, ImpPagar: 485,
			TipoCambio: &retentionExchangeInput{Factor: 3.8}, // manual sin fecha: hereda la del comprobante
		},
	}, "2026-09-05T09:00:00-05:00")
	if err != nil {
		t.Fatal(err)
	}
	rp := &facturador.RetentionPayload{FechaEmision: "2026-09-05T09:00:00-05:00", Regimen: "01", Tasa: 3, ImpRetenido: 45, ImpPagado: 1455, Details: rdet}
	rf := assertAllDatesMatchContract(t, rp)
	if len(rf["fecha"]) != 4 { // 2 pagos + 2 tipoCambio
		t.Fatalf("fechas pago/tipoCambio: %v", rf["fecha"])
	}
	if day(rdet[0].TipoCambio.Fecha) != "2026-09-04" || rdet[0].TipoCambio.Factor != 3.75 {
		t.Errorf("TC automático: %+v", rdet[0].TipoCambio)
	}
	if day(rdet[1].TipoCambio.Fecha) != "2026-09-04" || rdet[1].TipoCambio.Factor != 3.8 {
		t.Errorf("TC manual sin fecha debía heredar la del comprobante (4-sep Lima): %+v", rdet[1].TipoCambio)
	}
	if len(*asked) != 1 || (*asked)[0] != "2026-09-04" {
		t.Errorf("el TC se consulta por día AAAA-MM-DD: %v", *asked)
	}

	pdet, err := s.buildPerceptionDetails([]perceptionDetailBuildInput{
		{
			TipoDoc: "01", NumDoc: "F001-9", FechaEmision: "2026-12-31", ImpTotal: 2000, Moneda: "USD",
			Cobros:          []retentionPaymentInput{{Moneda: "USD", Importe: 1500, Fecha: "2026-12-31"}, {Moneda: "USD", Importe: 500, Fecha: "2027-01-01"}},
			FechaPercepcion: "2027-01-01", ImpPercibido: 40, ImpCobrar: 1960,
			TipoCambio: &retentionExchangeInput{Factor: 3.9, Fecha: "2026-12-31"},
		},
	}, "2027-01-01T08:00:00-05:00")
	if err != nil {
		t.Fatal(err)
	}
	pp := &facturador.PerceptionPayload{FechaEmision: "2027-01-01T08:00:00-05:00", Regimen: "01", Tasa: 2, ImpPercibido: 40, ImpCobrado: 1960, Details: pdet}
	pf := assertAllDatesMatchContract(t, pp)
	if got := pf["fecha"]; len(got) != 3 || day(got[0]) != "2026-12-31" || day(got[1]) != "2027-01-01" || day(got[2]) != "2026-12-31" {
		t.Errorf("fechas de cobros/TC con cruce de año: %v", got)
	}
}
