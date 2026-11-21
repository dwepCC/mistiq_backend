package service

import (
	"tukifac/pkg/database"
	"tukifac/pkg/money"
)

// BankAccountReconcileRow cuadre de una cuenta: el saldo guardado frente a lo que explican sus movimientos.
// ImpliedOpening = saldo − neto de movimientos (en soles): es el "saldo inicial" que cuadraría. Una cuenta creada en cero debe
// dar ~0; una creada con saldo inicial debe dar ese saldo. Una diferencia que no se explica por ninguno de los dos indica
// un desvío (movimiento sin saldo, saldo editado a mano, cobros en dólares anteriores a la corrección de moneda, etc.).
type BankAccountReconcileRow struct {
	AccountID      uint    `json:"account_id"`
	Name           string  `json:"name"`
	Currency       string  `json:"currency"`
	Balance        float64 `json:"balance"`
	Credits        float64 `json:"credits"`
	Debits         float64 `json:"debits"`
	Movements      int64   `json:"movements"`
	ImpliedOpening float64 `json:"implied_opening"`
	// UnvaluedUSD: movimientos de documentos en dólares sin equivalente en soles (no se pueden valuar).
	UnvaluedUSD int64 `json:"unvalued_usd"`
}

// ReconcileBankAccounts calcula, solo lectura, el cuadre de cada cuenta bancaria activa o no.
func (s *CashBankService) ReconcileBankAccounts() ([]BankAccountReconcileRow, error) {
	var accs []database.TenantBankAccount
	if err := s.db.Unscoped().Order("id ASC").Find(&accs).Error; err != nil {
		return nil, err
	}
	out := make([]BankAccountReconcileRow, 0, len(accs))
	for _, a := range accs {
		var agg struct {
			Credits float64
			Debits  float64
			N       int64
			NoPEN   int64
		}
		if err := s.db.Table("tenant_bank_movements").
			Select(`COALESCE(SUM(CASE WHEN type = 'credit' THEN COALESCE(account_amount, amount_pen, amount) ELSE 0 END), 0) AS credits,
				COALESCE(SUM(CASE WHEN type = 'debit' THEN COALESCE(account_amount, amount_pen, amount) ELSE 0 END), 0) AS debits,
				COUNT(*) AS n,
				COALESCE(SUM(CASE WHEN currency = 'USD' AND amount_pen IS NULL THEN 1 ELSE 0 END), 0) AS no_pen`).
			Where("bank_account_id = ?", a.ID).Scan(&agg).Error; err != nil {
			return nil, err
		}
		out = append(out, BankAccountReconcileRow{
			AccountID: a.ID, Name: a.Name, Currency: a.Currency, Balance: money.RoundDisplay(a.Balance),
			Credits: money.RoundDisplay(agg.Credits), Debits: money.RoundDisplay(agg.Debits), Movements: agg.N,
			ImpliedOpening: money.RoundDisplay(a.Balance - agg.Credits + agg.Debits), UnvaluedUSD: agg.NoPEN,
		})
	}
	return out, nil
}
