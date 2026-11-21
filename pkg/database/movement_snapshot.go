package database

import (
	"strings"
	"time"

	"tukifac/pkg/branchscope"
	"tukifac/pkg/dailyfx"
	"tukifac/pkg/datespe"

	"gorm.io/gorm"
)

// Snapshot de moneda de cada movimiento de caja/banco (V182). El monto del movimiento está en la moneda del documento de origen
// (venta/compra); al crearlo se guarda además su moneda, el TC con el que se valuó y su **equivalente en soles** (AmountPEN), de modo
// que el saldo, el arqueo y los reportes no dependan de recalcular con datos que luego puedan cambiar. Los movimientos sin documento
// (manuales) o de documentos en soles son en soles. Si el documento es USD y no hay ningún TC, AmountPEN queda nil y los lectores
// derivan el valor (como antes de V182).

type docMoneyInfo struct {
	Currency     string
	ExchangeRate *float64
	IssueDate    time.Time
}

// movementSnapshot calcula (moneda, TC, equivalente en soles) para un movimiento de importe positivo `amount`.
func movementSnapshot(tx *gorm.DB, saleID, purchaseID *uint, amount float64) (currency string, rate *float64, pen *float64) {
	pesos := amount
	currency = "PEN"
	if (saleID == nil || *saleID == 0) && (purchaseID == nil || *purchaseID == 0) {
		return currency, nil, &pesos
	}
	var info docMoneyInfo
	q := branchscope.System(tx.Session(&gorm.Session{NewDB: true}), "snapshot de moneda de movimiento")
	var err error
	switch {
	case saleID != nil && *saleID > 0:
		err = q.Table("tenant_sales").Select("currency, exchange_rate, issue_date").Where("id = ?", *saleID).Take(&info).Error
	default:
		err = q.Table("tenant_purchases").Select("currency, exchange_rate, issue_date").Where("id = ?", *purchaseID).Take(&info).Error
	}
	if err != nil {
		return currency, nil, &pesos // documento no legible (p. ej. pruebas con esquema reducido): se trata como soles
	}
	if strings.ToUpper(strings.TrimSpace(info.Currency)) != "USD" {
		return currency, nil, &pesos
	}
	currency = "USD"
	switch {
	case info.ExchangeRate != nil && *info.ExchangeRate > 0:
		rate = info.ExchangeRate
	default:
		if r, _, ok := dailyfx.SaleRate(info.IssueDate.In(datespe.Location()).Format("2006-01-02")); ok && r > 0 {
			rate = &r
		}
	}
	if rate == nil {
		return currency, nil, nil
	}
	v := roundCents(amount * *rate)
	return currency, rate, &v
}

func roundCents(v float64) float64 {
	if v < 0 {
		return -roundCents(-v)
	}
	return float64(int64(v*100+0.5)) / 100
}

// BeforeCreate completa el snapshot de moneda si quien crea el movimiento no lo trajo.
func (m *TenantCashMovement) BeforeCreate(tx *gorm.DB) error {
	if m.AmountPEN == nil {
		cur, rate, pen := movementSnapshot(tx, m.SaleID, m.PurchaseID, m.Amount)
		m.Currency, m.ExchangeRate, m.AmountPEN = cur, rate, pen
	}
	return nil
}

// BeforeCreate completa el snapshot de moneda si quien crea el movimiento no lo trajo.
func (m *TenantBankMovement) BeforeCreate(tx *gorm.DB) error {
	if m.AmountPEN == nil && m.AccountAmount == nil {
		cur, rate, pen := movementSnapshot(tx, m.SaleID, m.PurchaseID, m.Amount)
		m.Currency, m.ExchangeRate, m.AmountPEN = cur, rate, pen
	}
	if m.AccountAmount == nil {
		m.setAccountAmount(tx)
	}
	return nil
}

// officialRate devuelve el TC venta guardado para el día de la fecha dada (hoy si es cero).
func officialRate(t time.Time) (float64, bool) {
	if t.IsZero() {
		t = time.Now()
	}
	r, _, ok := dailyfx.SaleRate(t.In(datespe.Location()).Format("2006-01-02"))
	return r, ok && r > 0
}

// setAccountAmount calcula AccountAmount: lo que el movimiento suma al saldo de la cuenta, en la MONEDA DE LA CUENTA.
//   - Movimiento manual (sin documento ni transferencia): el importe ya está en la moneda de la cuenta.
//   - Con documento: cuenta en soles → equivalente en soles; cuenta en dólares → el importe si el documento es USD, o el importe en
//     soles ÷ TC oficial del día si es PEN.
//
// Si no se puede convertir (falta el TC) queda nil y quien actualiza el saldo lo rechaza.
func (m *TenantBankMovement) setAccountAmount(tx *gorm.DB) {
	var accCur string
	q := branchscope.System(tx.Session(&gorm.Session{NewDB: true}), "moneda de la cuenta del movimiento")
	if err := q.Table("tenant_bank_accounts").Select("currency").Where("id = ?", m.BankAccountID).Scan(&accCur).Error; err != nil {
		return
	}
	accCur = strings.ToUpper(strings.TrimSpace(accCur))
	manual := (m.SaleID == nil || *m.SaleID == 0) && (m.PurchaseID == nil || *m.PurchaseID == 0) && m.TransferID == nil
	if accCur != "USD" {
		m.AccountAmount = m.AmountPEN // cuenta en soles (o etiqueta histórica sin efecto): el saldo va en soles
		return
	}
	switch {
	case manual:
		// Movimiento manual en una cuenta en dólares: el importe tecleado ya está en dólares.
		v := m.Amount
		m.AccountAmount = &v
		m.Currency = "USD"
		m.ExchangeRate, m.AmountPEN = nil, nil
		if r, ok := officialRate(m.Date); ok {
			rr := r
			p := roundCents(m.Amount * r)
			m.ExchangeRate, m.AmountPEN = &rr, &p
		}
	case m.Currency == "USD":
		v := m.Amount
		m.AccountAmount = &v
	default: // documento en soles hacia una cuenta en dólares: se divide por el TC oficial del día
		if r, ok := officialRate(m.Date); ok && m.AmountPEN != nil {
			v := roundCents(*m.AmountPEN / r)
			m.AccountAmount = &v
			rr := r
			m.ExchangeRate = &rr
		}
	}
}

// BalanceDelta devuelve cuánto debe cambiar el saldo de la cuenta (siempre en soles) por este movimiento. delta llega con el signo
// correcto y en la moneda del documento. Si el movimiento se valuó (AmountPEN) se usa su equivalente en soles. ok=false cuando el
// documento es USD y no se pudo valuar (sin TC): no se puede acreditar la cuenta sin sumar dólares como soles.
func (m *TenantBankMovement) BalanceDelta(delta float64) (float64, bool) {
	if m == nil || m.Amount == 0 {
		return delta, true
	}
	if m.AccountAmount != nil { // V183: ya viene en la moneda de la cuenta
		if delta < 0 {
			return -*m.AccountAmount, true
		}
		return *m.AccountAmount, true
	}
	if m.AmountPEN == nil {
		if m.Currency == "USD" {
			return delta, false
		}
		return delta, true
	}
	if delta < 0 {
		return -*m.AmountPEN, true
	}
	return *m.AmountPEN, true
}
