package service

import (
	"strings"
	"time"

	"tukifac/pkg/dailyfx"
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/salecurrency"
)

// rateForDay devuelve (con caché por día) el TC venta guardado para un día; estimated siempre true (no es el de la venta).
type dayRateCache map[string]*float64

func (c dayRateCache) get(day string) *float64 {
	if v, ok := c[day]; ok {
		return v
	}
	var out *float64
	if r, _, ok := dailyfx.SaleRate(day); ok && r > 0 {
		out = &r
	}
	c[day] = out
	return out
}

func saleCurrencyOf(s *database.TenantSale) string {
	if c := strings.ToUpper(strings.TrimSpace(s.Currency)); c != "" {
		return c
	}
	return salecurrency.CurrencyPEN
}

// fillSalesPEN calcula el equivalente en soles de cada venta del listado (total × TC de la venta; si una venta en USD no
// guardó TC, el TC del día de emisión, marcado RateEstimated). Solo lectura: no modifica las ventas.
func fillSalesPEN(sales []database.TenantSale) {
	cache := dayRateCache{}
	for i := range sales {
		s := &sales[i]
		if saleCurrencyOf(s) != salecurrency.CurrencyUSD {
			v := s.Total
			s.TotalPEN = &v
			continue
		}
		var rate float64
		if s.ExchangeRate != nil && *s.ExchangeRate > 0 {
			rate = *s.ExchangeRate
		} else if r := cache.get(salecurrency.DayOf(s.IssueDate)); r != nil {
			rate = *r
			s.RateEstimated = true
		} else {
			continue // sin ningún TC: queda sin equivalente
		}
		pen := money.RoundDisplay(s.Total * rate)
		s.TotalPEN = &pen
		r := rate
		s.RateEffective = &r
	}
}

// estimateMissingRatePEN suma en soles las ventas en USD **sin TC guardado** (fecha + importe) con el TC del día de
// emisión. Devuelve el total convertido, cuántas se estimaron y cuántas siguen sin poderse convertir.
func estimateMissingRatePEN(rows []struct {
	IssueDate time.Time
	Total     float64
}) (pen float64, estimated, unconvertible int64) {
	cache := dayRateCache{}
	for _, r := range rows {
		rate := cache.get(salecurrency.DayOf(r.IssueDate))
		if rate == nil {
			unconvertible++
			continue
		}
		pen += r.Total * *rate
		estimated++
	}
	return money.RoundDisplay(pen), estimated, unconvertible
}
