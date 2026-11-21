package service

import (
	"sort"
	"strings"
	"time"

	"tukifac/pkg/dailyfx"
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/salecurrency"
)

// Moneda de los movimientos de caja/banco en los reportes. El movimiento no guarda moneda: se **deriva** de la venta o
// compra de origen (los manuales y los sin documento son en soles). El monto del movimiento está en la moneda del
// documento; su equivalente en soles es monto × TC del documento (si el documento no guardó TC, el TC del día de
// emisión, marcado RateEstimated; si tampoco hay, queda sin equivalente y fuera del total en soles).

type movementDocCurrency struct {
	currency string
	rate     *float64
	issued   time.Time
}

func normCurrency(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	if c == "" {
		return salecurrency.CurrencyPEN
	}
	return c
}

// enrichMovementRowsCurrency completa Currency/ExchangeRate/AmountPEN de cada fila a partir de su venta o compra de origen.
func (s *CashBankService) enrichMovementRowsCurrency(rows []MovementReportRow) {
	saleIDs := map[uint]struct{}{}
	purchaseIDs := map[uint]struct{}{}
	for _, r := range rows {
		if r.SaleID != nil && *r.SaleID > 0 {
			saleIDs[*r.SaleID] = struct{}{}
		}
		if r.PurchaseID != nil && *r.PurchaseID > 0 {
			purchaseIDs[*r.PurchaseID] = struct{}{}
		}
	}
	sales := map[uint]movementDocCurrency{}
	if len(saleIDs) > 0 {
		ids := make([]uint, 0, len(saleIDs))
		for id := range saleIDs {
			ids = append(ids, id)
		}
		var docs []database.TenantSale
		if s.db.Unscoped().Select("id", "currency", "exchange_rate", "issue_date").Where("id IN ?", ids).Find(&docs).Error == nil {
			for _, d := range docs {
				sales[d.ID] = movementDocCurrency{currency: normCurrency(d.Currency), rate: d.ExchangeRate, issued: d.IssueDate}
			}
		}
	}
	purchases := map[uint]movementDocCurrency{}
	if len(purchaseIDs) > 0 {
		ids := make([]uint, 0, len(purchaseIDs))
		for id := range purchaseIDs {
			ids = append(ids, id)
		}
		var docs []database.TenantPurchase
		if s.db.Unscoped().Select("id", "currency", "exchange_rate", "issue_date").Where("id IN ?", ids).Find(&docs).Error == nil {
			for _, d := range docs {
				purchases[d.ID] = movementDocCurrency{currency: normCurrency(d.Currency), rate: d.ExchangeRate, issued: d.IssueDate}
			}
		}
	}
	dayRates := map[string]*float64{}
	dayRate := func(t time.Time) *float64 {
		day := salecurrency.DayOf(t)
		if v, ok := dayRates[day]; ok {
			return v
		}
		var out *float64
		if r, _, ok := dailyfx.SaleRate(day); ok && r > 0 {
			out = &r
		}
		dayRates[day] = out
		return out
	}
	for i := range rows {
		r := &rows[i]
		doc := movementDocCurrency{currency: salecurrency.CurrencyPEN}
		switch {
		case r.SaleID != nil && sales[*r.SaleID].currency != "":
			doc = sales[*r.SaleID]
		case r.PurchaseID != nil && purchases[*r.PurchaseID].currency != "":
			doc = purchases[*r.PurchaseID]
		}
		r.Currency = doc.currency
		if doc.currency != salecurrency.CurrencyUSD {
			r.AmountPEN = r.Amount
			continue
		}
		switch {
		case doc.rate != nil && *doc.rate > 0:
			r.ExchangeRate = doc.rate
		default:
			if est := dayRate(doc.issued); est != nil {
				r.ExchangeRate = est
				r.RateEstimated = true
			}
		}
		if r.ExchangeRate == nil {
			r.RateMissing = true
			continue
		}
		r.AmountPEN = money.RoundDisplay(r.Amount * *r.ExchangeRate)
	}
}

// movementAmountPEN equivalente en soles de una fila; las filas sin enriquecer (Currency vacío) son soles.
func movementAmountPEN(r MovementReportRow) float64 {
	if r.Currency == "" {
		return r.Amount
	}
	return r.AmountPEN
}

// MovementCurrencyTotal ingresos/egresos/neto de UNA moneda (en su moneda) y su equivalente en soles.
type MovementCurrencyTotal struct {
	Currency      string  `json:"currency"`
	Rows          int64   `json:"rows"`
	SumIncome     float64 `json:"sum_income"`
	SumExpense    float64 `json:"sum_expense"`
	NetMovement   float64 `json:"net_movement"`
	SumIncomePEN  float64 `json:"sum_income_pen"`
	SumExpensePEN float64 `json:"sum_expense_pen"`
	NetPEN        float64 `json:"net_pen"`
	// Transferencias internas (no son ingreso ni egreso; sí cuentan en el neto): entradas y salidas, en soles.
	TransfersInPEN  float64 `json:"transfers_in_pen"`
	TransfersOutPEN float64 `json:"transfers_out_pen"`
}

// isTransferRow movimiento que forma parte de una transferencia entre caja y cuentas (o de su anulación).
func isTransferRow(r MovementReportRow) bool {
	return r.Category == "Transferencia" || r.Category == "Anulación transferencia"
}

// movementTotalsByCurrency separa los movimientos por moneda (sin mezclar) y suma sus equivalentes en soles.
func movementTotalsByCurrency(rows []MovementReportRow) []MovementCurrencyTotal {
	by := map[string]*MovementCurrencyTotal{}
	for _, r := range rows {
		cur := normCurrency(r.Currency)
		t := by[cur]
		if t == nil {
			t = &MovementCurrencyTotal{Currency: cur}
			by[cur] = t
		}
		t.Rows++
		pen := movementAmountPEN(r)
		if isTransferRow(r) {
			if r.Amount >= 0 {
				t.TransfersInPEN += pen
			} else {
				t.TransfersOutPEN += -pen
			}
			continue
		}
		if r.Amount >= 0 {
			t.SumIncome += r.Amount
			t.SumIncomePEN += pen
		} else {
			t.SumExpense += -r.Amount
			t.SumExpensePEN += -pen
		}
	}
	out := make([]MovementCurrencyTotal, 0, len(by))
	for _, t := range by {
		t.SumIncome = money.RoundDisplay(t.SumIncome)
		t.SumExpense = money.RoundDisplay(t.SumExpense)
		t.TransfersInPEN = money.RoundDisplay(t.TransfersInPEN)
		t.TransfersOutPEN = money.RoundDisplay(t.TransfersOutPEN)
		t.NetMovement = money.RoundDisplay(t.SumIncome - t.SumExpense + t.TransfersInPEN - t.TransfersOutPEN)
		t.SumIncomePEN = money.RoundDisplay(t.SumIncomePEN)
		t.SumExpensePEN = money.RoundDisplay(t.SumExpensePEN)
		t.NetPEN = money.RoundDisplay(t.SumIncomePEN - t.SumExpensePEN + t.TransfersInPEN - t.TransfersOutPEN)
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Currency == salecurrency.CurrencyPEN {
			return true
		}
		if out[j].Currency == salecurrency.CurrencyPEN {
			return false
		}
		return out[i].Currency < out[j].Currency
	})
	return out
}

// ApplyCurrencySummary completa en un resumen los totales por moneda, el consolidado en soles y los avisos de TC a partir de
// las filas (ya enriquecidas). Lo usan los bloques por canal y el resumen plano del reporte de movimientos.
func ApplyCurrencySummary(summary *MovementChannelSummary, rows []MovementReportRow) {
	summary.ByCurrency = movementTotalsByCurrency(rows)
	summary.SumIncomePEN, summary.SumExpensePEN, summary.TransfersInPEN, summary.TransfersOutPEN = 0, 0, 0, 0
	for _, c := range summary.ByCurrency {
		summary.SumIncomePEN += c.SumIncomePEN
		summary.SumExpensePEN += c.SumExpensePEN
		summary.TransfersInPEN += c.TransfersInPEN
		summary.TransfersOutPEN += c.TransfersOutPEN
	}
	summary.SumIncomePEN = money.RoundDisplay(summary.SumIncomePEN)
	summary.SumExpensePEN = money.RoundDisplay(summary.SumExpensePEN)
	summary.TransfersInPEN = money.RoundDisplay(summary.TransfersInPEN)
	summary.TransfersOutPEN = money.RoundDisplay(summary.TransfersOutPEN)
	// El neto (y por tanto el saldo físico) SÍ incluye las transferencias: el efectivo realmente entra o sale de la caja.
	summary.NetMovementPEN = money.RoundDisplay(summary.SumIncomePEN - summary.SumExpensePEN + summary.TransfersInPEN - summary.TransfersOutPEN)
	summary.RateEstimatedCount, summary.RateMissingCount = 0, 0
	for _, r := range rows {
		if r.RateEstimated {
			summary.RateEstimatedCount++
		}
		if r.RateMissing {
			summary.RateMissingCount++
		}
	}
}
