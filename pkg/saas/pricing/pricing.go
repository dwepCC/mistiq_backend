package pricing

import (
	"errors"
	"math"
	"strings"
	"time"
)

// BillableMonths meses a cobrar de una suscripción.
//
// Manda `billedMonths` (los meses que de verdad se vendieron). Solo si viene en 0 —dato de
// antes de que ese campo existiera— se cae al tramo start→end, que es una aproximación: en una
// renovación anticipada la vigencia se encadena al período anterior y esa distancia es mayor
// que lo contratado.
func BillableMonths(billedMonths int, start, end time.Time, loc *time.Location) int {
	if billedMonths > 0 {
		return billedMonths
	}
	m := MonthsBetween(start, end, loc)
	if m < 1 {
		return 1
	}
	return m
}

// MonthsBetween meses completos entre dos fechas, contados por calendario en `loc`.
//
// De 13/07 a 13/09 son 2 meses aunque sumen 62 días; un resto de días cuenta como mes
// empezado (13/07 → 15/09 = 3) porque ese tramo también está cubierto. Vive aquí para que
// el cobro se calcule igual venga de donde venga el ciclo.
func MonthsBetween(start, end time.Time, loc *time.Location) int {
	s := dayIn(start, loc)
	e := dayIn(end, loc)
	if !e.After(s) {
		return 0
	}
	months := (e.Year()-s.Year())*12 + int(e.Month()) - int(s.Month())
	if e.Day() > s.Day() {
		months++
	}
	if months < 1 {
		months = 1
	}
	return months
}

func dayIn(t time.Time, loc *time.Location) time.Time {
	v := t.In(loc)
	y, m, d := v.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// Tipos de descuento admitidos en una suscripción.
const (
	DiscountNone    = ""
	DiscountPercent = "percent"
	DiscountFixed   = "fixed"
)

// Discount descuento pactado con el cliente (típicamente por contratar varios meses).
type Discount struct {
	Type  string  `json:"discount_type"`
	Value float64 `json:"discount_value"`
}

// ErrInvalidDiscount descuento mal formado.
var ErrInvalidDiscount = errors.New("descuento inválido")

// NormalizeDiscount valida y limpia el descuento. Un tipo desconocido o un valor <= 0 se
// tratan como «sin descuento» en vez de fallar, para que un dato viejo no rompa un cobro.
func NormalizeDiscount(d Discount) (Discount, error) {
	t := strings.ToLower(strings.TrimSpace(d.Type))
	if t != DiscountPercent && t != DiscountFixed {
		return Discount{}, nil
	}
	if d.Value <= 0 {
		return Discount{}, nil
	}
	if t == DiscountPercent && d.Value > 100 {
		return Discount{}, ErrInvalidDiscount
	}
	return Discount{Type: t, Value: round2(d.Value)}, nil
}

// CycleAmounts importe de un cobro: bruto (precio × meses) y neto tras el descuento.
type CycleAmounts struct {
	Months   int
	Gross    float64
	Discount Discount
	Net      float64
}

// ComputeCycleAmounts calcula lo que hay que cobrar por `months` meses del plan.
//
// El precio del plan es MENSUAL, así que contratar 6 meses cuesta seis veces el plan. Antes
// el cobro se emitía por el precio del plan a secas sin importar la duración, de modo que una
// suscripción de seis meses se saldaba pagando uno.
func ComputeCycleAmounts(monthlyPrice float64, months int, d Discount) CycleAmounts {
	if months < 1 {
		months = 1
	}
	gross := round2(monthlyPrice * float64(months))
	norm, err := NormalizeDiscount(d)
	if err != nil {
		norm = Discount{}
	}
	net := gross - discountAmount(gross, norm)
	if net < 0 {
		net = 0
	}
	return CycleAmounts{Months: months, Gross: gross, Discount: norm, Net: round2(net)}
}

// discountAmount importe descontado (nunca mayor que el bruto).
func discountAmount(gross float64, d Discount) float64 {
	switch d.Type {
	case DiscountPercent:
		return round2(gross * d.Value / 100)
	case DiscountFixed:
		if d.Value > gross {
			return gross
		}
		return round2(d.Value)
	default:
		return 0
	}
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
