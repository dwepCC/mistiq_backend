package pricing

import (
	"testing"
	"time"
)

var lima = time.FixedZone("America/Lima", -5*3600)

// El precio del plan es mensual: contratar varios meses cuesta esas veces el plan. Antes el
// cobro se emitía por el precio del plan a secas, así que 6 meses se saldaban pagando uno.
func TestComputeCycleAmountsEscalaPorMeses(t *testing.T) {
	casos := []struct {
		months int
		want   float64
	}{
		{1, 99},
		{3, 297},
		{6, 594},
		{12, 1188},
		{0, 99}, // months inválido se trata como 1, nunca como cobro cero
	}
	for _, c := range casos {
		got := ComputeCycleAmounts(99, c.months, Discount{})
		if got.Net != c.want || got.Gross != c.want {
			t.Errorf("%d meses: neto=%.2f bruto=%.2f, se esperaba %.2f", c.months, got.Net, got.Gross, c.want)
		}
	}
}

func TestComputeCycleAmountsConDescuento(t *testing.T) {
	casos := []struct {
		nombre    string
		months    int
		discount  Discount
		wantGross float64
		wantNet   float64
	}{
		{"10% sobre 6 meses", 6, Discount{DiscountPercent, 10}, 594, 534.60},
		{"monto fijo de 100", 12, Discount{DiscountFixed, 100}, 1188, 1088},
		{"sin descuento", 3, Discount{}, 297, 297},
		{"tipo desconocido se ignora", 3, Discount{"regalo", 50}, 297, 297},
		{"valor 0 se ignora", 3, Discount{DiscountPercent, 0}, 297, 297},
		// Un fijo mayor que el cobro deja el importe en 0, nunca negativo.
		{"fijo mayor que el bruto", 1, Discount{DiscountFixed, 500}, 99, 0},
		{"100% deja el cobro en cero", 6, Discount{DiscountPercent, 100}, 594, 0},
	}
	for _, c := range casos {
		got := ComputeCycleAmounts(99, c.months, c.discount)
		if got.Gross != c.wantGross || got.Net != c.wantNet {
			t.Errorf("%s: bruto=%.2f neto=%.2f, se esperaba bruto=%.2f neto=%.2f",
				c.nombre, got.Gross, got.Net, c.wantGross, c.wantNet)
		}
	}
}

// Un porcentaje por encima de 100 es un error de captura, no un regalo.
func TestNormalizeDiscountRechazaPorcentajeImposible(t *testing.T) {
	if _, err := NormalizeDiscount(Discount{DiscountPercent, 150}); err == nil {
		t.Error("se esperaba error para un descuento del 150%")
	}
}

// Los planes gratis existen y deben poder facturarse en 0 sin romper nada.
func TestComputeCycleAmountsPlanGratis(t *testing.T) {
	got := ComputeCycleAmounts(0, 12, Discount{})
	if got.Gross != 0 || got.Net != 0 || got.Months != 12 {
		t.Errorf("plan gratis: bruto=%.2f neto=%.2f meses=%d", got.Gross, got.Net, got.Months)
	}
}

func TestMonthsBetween(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time {
		return time.Date(y, m, day, 0, 0, 0, 0, lima)
	}
	casos := []struct {
		nombre     string
		start, end time.Time
		want       int
	}{
		{"un mes exacto", d(2026, 8, 4), d(2026, 9, 4), 1},
		{"seis meses exactos", d(2026, 8, 4), d(2027, 2, 4), 6},
		{"un año", d(2026, 8, 4), d(2027, 8, 4), 12},
		{"resto de días cuenta como mes empezado", d(2026, 7, 13), d(2026, 9, 15), 3},
		{"fin anterior al inicio", d(2026, 9, 4), d(2026, 8, 4), 0},
		{"tramo menor a un mes cuenta como uno", d(2026, 8, 4), d(2026, 8, 20), 1},
	}
	for _, c := range casos {
		if got := MonthsBetween(c.start, c.end, lima); got != c.want {
			t.Errorf("%s: %d meses, se esperaba %d", c.nombre, got, c.want)
		}
	}
}

// Al renovar antes de tiempo la vigencia se encadena al fin del período anterior mientras
// start_date es hoy, así que el tramo start→end supera lo vendido. Cobrar por esa distancia
// inflaba la factura: 6 meses vendidos salían como 9.
func TestBillableMonthsUsaLoVendidoNoElTramo(t *testing.T) {
	start := time.Date(2026, 8, 4, 0, 0, 0, 0, lima) // alta hoy
	end := time.Date(2027, 4, 30, 0, 0, 0, 0, lima)  // encadenado tras un período previo
	if got := MonthsBetween(start, end, lima); got != 9 {
		t.Fatalf("premisa del test: el tramo debía dar 9 meses, dio %d", got)
	}
	if got := BillableMonths(6, start, end, lima); got != 6 {
		t.Errorf("con 6 meses vendidos se cobraron %d", got)
	}
	// Sin el dato (suscripciones anteriores al campo) se cae al tramo.
	if got := BillableMonths(0, start, end, lima); got != 9 {
		t.Errorf("sin billed_months debía caer al tramo (9), dio %d", got)
	}
}
