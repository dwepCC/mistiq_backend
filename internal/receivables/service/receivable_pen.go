package service

import (
	"strings"
	"time"

	"tukifac/pkg/dailyfx"
	"tukifac/pkg/salecurrency"
)

// Equivalente en SOLES de los saldos por cobrar. Los importes de cada documento quedan en su moneda; para consolidar (KPI y estado
// de cuenta) un documento en USD se convierte con el TC con el que se registró (si no lo guardó, el TC del día de emisión, marcado
// como estimado; si tampoco hay, no se puede convertir y se avisa). Los documentos en soles no cambian.

type penConverter struct{ dayRates map[string]*float64 }

func newPENConverter() *penConverter { return &penConverter{dayRates: map[string]*float64{}} }

// factor devuelve el multiplicador a soles de un documento. ok=false si es USD y no hay ningún TC; estimated=true si se usó el del día.
func (c *penConverter) factor(currency string, rate *float64, issued time.Time) (f float64, estimated, ok bool) {
	if strings.ToUpper(strings.TrimSpace(currency)) != salecurrency.CurrencyUSD {
		return 1, false, true
	}
	if rate != nil && *rate > 0 {
		return *rate, false, true
	}
	day := salecurrency.DayOf(issued)
	r, seen := c.dayRates[day]
	if !seen {
		if v, _, found := dailyfx.SaleRate(day); found && v > 0 {
			r = &v
		}
		c.dayRates[day] = r
	}
	if r == nil {
		return 0, false, false
	}
	return *r, true, true
}
