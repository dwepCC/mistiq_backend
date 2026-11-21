package detraccion

import (
	"math"
	"testing"
)

func sum(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return math.Round(s*100) / 100
}

func TestNetInstallmentAmounts_igualesDanLasMismasCuotasQueUnaVentaNormal(t *testing.T) {
	// Factura 2000, detracción 240: cobro 3 x 666.67 (=2000.01 → la última ajusta). Fiscal = neto 1760 en 3 cuotas.
	got := NetInstallmentAmounts([]float64{666.67, 666.67, 666.66}, 240)
	if sum(got) != 1760 {
		t.Fatalf("suma fiscal = %v, want 1760: %v", sum(got), got)
	}
	if got[0] != 586.67 || got[1] != 586.67 || got[2] != 586.66 {
		t.Fatalf("cuotas = %v, want [586.67 586.67 586.66] (igual que 1760/3)", got)
	}
}

func TestNetInstallmentAmounts_conPagoDirectoDescuentaLaDetraccionDelSaldo(t *testing.T) {
	// Total 2000, pago directo 500, cuotas de cobro suman 1500; fiscal = 1500 - 240 = 1260.
	got := NetInstallmentAmounts([]float64{750, 750}, 240)
	if sum(got) != 1260 {
		t.Fatalf("suma fiscal = %v, want 1260: %v", sum(got), got)
	}
}

func TestNetInstallmentAmounts_desigualesConservanLaProporcionYLaSuma(t *testing.T) {
	got := NetInstallmentAmounts([]float64{1000, 500, 500}, 240)
	if sum(got) != 1760 || got[0] <= got[1] {
		t.Fatalf("cuotas = %v, deben sumar 1760 y mantener la proporción", got)
	}
}

func TestNetInstallmentAmounts_sinAjusteCuandoNoAplica(t *testing.T) {
	in := []float64{100, 100}
	for _, det := range []float64{0, -5, 200, 500} { // detracción nula, negativa o >= suma
		got := NetInstallmentAmounts(in, det)
		if got[0] != 100 || got[1] != 100 {
			t.Fatalf("det=%v debe dejar las cuotas igual: %v", det, got)
		}
	}
	if len(NetInstallmentAmounts(nil, 240)) != 0 {
		t.Fatal("sin cuotas no devuelve nada")
	}
	in[0] = 1
	if NetInstallmentAmounts([]float64{5}, 1)[0] != 4 {
		t.Fatal("una sola cuota: total - detracción")
	}
}
