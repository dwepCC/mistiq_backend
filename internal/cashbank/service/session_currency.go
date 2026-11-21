package service

import (
	"strings"
	"time"

	"tukifac/pkg/dailyfx"
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/salecurrency"
)

// Cierre/arqueo y resumen de una sesión de caja en SOLES. Los movimientos de caja/banco ligados a una venta o compra en USD
// están en la moneda del documento; para el saldo, el esperado del arqueo y los totales por método se usa su equivalente
// en soles (monto × TC del documento; si no guardó TC, el del día de emisión). Los movimientos manuales y de documentos en
// soles quedan igual, así que una sesión solo en soles da exactamente los mismos números de siempre.

// sessionPEN equivalentes en soles (importes positivos) de los movimientos de una sesión, por id de movimiento.
type sessionPEN struct {
	cash       map[uint]float64
	bank       map[uint]float64
	foreign    bool  // hay al menos un movimiento de un documento en moneda extranjera
	estimated  int64 // movimientos en USD cuyo documento no guardó TC (se usó el TC del día)
	missing    int64 // movimientos en USD sin ningún TC (no suman)
	byCurrency []MovementCurrencyTotal
}

// penAmounts calcula el equivalente en soles de los movimientos dados (los de efectivo primero, luego los bancarios).
func (s *CashBankService) penAmounts(cash []database.TenantCashMovement, bank []database.TenantBankMovement) sessionPEN {
	out := sessionPEN{cash: map[uint]float64{}, bank: map[uint]float64{}}
	rows := make([]MovementReportRow, 0, len(cash)+len(bank))
	for _, m := range cash {
		amt := m.Amount
		if m.Type == "expense" {
			amt = -amt
		}
		rows = append(rows, MovementReportRow{Amount: amt, SaleID: m.SaleID, PurchaseID: m.PurchaseID})
	}
	for _, m := range bank {
		amt := m.Amount
		if m.Type != "credit" {
			amt = -amt
		}
		rows = append(rows, MovementReportRow{Amount: amt, SaleID: m.SaleID, PurchaseID: m.PurchaseID})
	}
	// Snapshot (V182): si el movimiento ya guarda su moneda/TC/equivalente se usa tal cual (no cambia aunque luego se edite el
	// documento); solo los que no lo tienen (anteriores a V182 sin TC, o sin ningún TC al crearse) se derivan del documento.
	var derive []int
	for i, m := range cash {
		if m.AmountPEN != nil {
			rows[i].Currency, rows[i].ExchangeRate, rows[i].AmountPEN = normCurrency(m.Currency), m.ExchangeRate, signedLike(rows[i].Amount, *m.AmountPEN)
		} else {
			derive = append(derive, i)
		}
	}
	for j, m := range bank {
		if m.AmountPEN != nil {
			rows[len(cash)+j].Currency, rows[len(cash)+j].ExchangeRate, rows[len(cash)+j].AmountPEN = normCurrency(m.Currency), m.ExchangeRate, signedLike(rows[len(cash)+j].Amount, *m.AmountPEN)
		} else {
			derive = append(derive, len(cash)+j)
		}
	}
	if len(derive) > 0 {
		sub := make([]MovementReportRow, len(derive))
		for k, idx := range derive {
			sub[k] = rows[idx]
		}
		s.enrichMovementRowsCurrency(sub)
		for k, idx := range derive {
			rows[idx] = sub[k]
		}
	}
	abs := func(v float64) float64 {
		if v < 0 {
			return -v
		}
		return v
	}
	for i, m := range cash {
		r := rows[i]
		out.cash[m.ID] = abs(movementAmountPEN(r))
		out.note(r)
	}
	for j, m := range bank {
		r := rows[len(cash)+j]
		out.bank[m.ID] = abs(movementAmountPEN(r))
		out.note(r)
	}
	out.byCurrency = movementTotalsByCurrency(rows)
	return out
}

// signedLike devuelve pen con el mismo signo que ref (los importes del snapshot se guardan en positivo).
func signedLike(ref, pen float64) float64 {
	if ref < 0 {
		return -pen
	}
	return pen
}

func (p *sessionPEN) note(r MovementReportRow) {
	if r.Currency != "" && r.Currency != salecurrency.CurrencyPEN {
		p.foreign = true
	}
	if r.RateEstimated {
		p.estimated++
	}
	if r.RateMissing {
		p.missing++
	}
}

// SessionCurrencySummary bloque informativo del reporte de sesión cuando hubo documentos en moneda extranjera: el saldo y los
// totales de la sesión en soles y los montos originales por moneda.
type SessionCurrencySummary struct {
	HasForeign bool `json:"has_foreign"`
	// Por moneda del documento (cada una en la suya) con su equivalente en soles; todos los métodos.
	ByCurrency []MovementCurrencyTotal `json:"by_currency,omitempty"`
	// Todo en soles (todos los métodos, cada USD al TC de su documento).
	TotalIncomePEN  float64 `json:"total_income_pen"`
	TotalExpensePEN float64 `json:"total_expense_pen"`
	// Efectivo físico en soles: apertura + ingresos − egresos en efectivo (el esperado del arqueo).
	CashIncomePEN      float64 `json:"cash_income_pen"`
	CashExpensePEN     float64 `json:"cash_expense_pen"`
	PhysicalBalancePEN float64 `json:"physical_balance_pen"`
	RateEstimatedCount int64   `json:"rate_estimated_count"`
	RateMissingCount   int64   `json:"rate_missing_count"`
}

// sessionCurrencySummary arma el bloque para una sesión. Vacío (HasForeign=false) si todo fue en soles.
func (s *CashBankService) sessionCurrencySummary(session *database.TenantCashSession) SessionCurrencySummary {
	var cash []database.TenantCashMovement
	s.db.Where("cash_session_id = ? AND type IN ?", session.ID, []string{"income", "expense"}).Find(&cash)
	var bank []database.TenantBankMovement
	s.db.Where("cash_session_id = ?", session.ID).Find(&bank)
	pen := s.penAmounts(cash, bank)
	if !pen.foreign {
		return SessionCurrencySummary{}
	}
	out := SessionCurrencySummary{HasForeign: true, ByCurrency: pen.byCurrency, RateEstimatedCount: pen.estimated, RateMissingCount: pen.missing}
	for _, m := range cash {
		v := pen.cash[m.ID]
		if m.Type == "income" {
			out.TotalIncomePEN += v
		} else {
			out.TotalExpensePEN += v
		}
		if IsCashPaymentMethod(m.PaymentMethod) {
			if m.Type == "income" {
				out.CashIncomePEN += v
			} else {
				out.CashExpensePEN += v
			}
		}
	}
	for _, m := range bank {
		v := pen.bank[m.ID]
		if m.Type == "credit" {
			out.TotalIncomePEN += v
		} else {
			out.TotalExpensePEN += v
		}
	}
	out.TotalIncomePEN = money.RoundDisplay(out.TotalIncomePEN)
	out.TotalExpensePEN = money.RoundDisplay(out.TotalExpensePEN)
	out.CashIncomePEN = money.RoundDisplay(out.CashIncomePEN)
	out.CashExpensePEN = money.RoundDisplay(out.CashExpensePEN)
	out.PhysicalBalancePEN = money.RoundDisplay(session.OpeningBalance + out.CashIncomePEN - out.CashExpensePEN)
	return out
}

// movementPEN importe en SOLES de un movimiento bancario sin documento (manual o de transferencia): el snapshot si existe (cuentas en
// dólares guardan su equivalente), o el importe tal cual (soles).
func movementPEN(m database.TenantBankMovement) float64 {
	if m.AmountPEN != nil {
		return *m.AmountPEN
	}
	return m.Amount
}

// docPENFactor multiplicador a soles de un documento: el TC guardado; si es USD sin TC, el TC del día de emisión; en soles (o sin
// ningún TC) 1. Lo usa el detalle de la sesión de caja para que TODAS sus filas y totales estén en soles.
func docPENFactor(currency string, rate *float64, issued time.Time) float64 {
	if strings.ToUpper(strings.TrimSpace(currency)) != "USD" {
		return 1
	}
	if rate != nil && *rate > 0 {
		return *rate
	}
	if r, _, ok := dailyfx.SaleRate(salecurrency.DayOf(issued)); ok && r > 0 {
		return r
	}
	return 1
}

// cashMovementPEN importe en soles de un movimiento de caja: el snapshot (amount_pen) si existe, o el importe tal cual.
func cashMovementPEN(m database.TenantCashMovement) float64 {
	if m.AmountPEN != nil {
		return *m.AmountPEN
	}
	return m.Amount
}
