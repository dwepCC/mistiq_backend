package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/dailyfx"
	"tukifac/pkg/database"
	"tukifac/pkg/datespe"
	"tukifac/pkg/money"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Transferencias entre la caja y las cuentas bancarias/billeteras (o entre dos cuentas).
//
// Cada transferencia es UNA operación atómica que crea dos movimientos enlazados por transfer_id —salida del origen y entrada al
// destino— y actualiza los saldos. A diferencia del sistema antiguo: valida saldo disponible, que origen y destino sean distintos,
// que las cuentas existan y estén activas, convierte entre monedas con un TC explícito y es anulable (movimientos inversos).

const (
	TransferKindCash = "cash"
	TransferKindBank = "bank"
)

type TransferEndpoint struct {
	Kind      string `json:"kind"`       // cash | bank
	AccountID uint   `json:"account_id"` // solo bank
}

type TransferInput struct {
	From TransferEndpoint `json:"from"`
	To   TransferEndpoint `json:"to"`
	// Amount: importe que SALE del origen, en la moneda del origen.
	Amount float64 `json:"amount"`
	// ExchangeRate: soles por dólar, obligatorio solo si las monedas difieren (si no viene se usa el TC oficial del día).
	ExchangeRate *float64   `json:"exchange_rate"`
	Date         *time.Time `json:"date"`
	Notes        string     `json:"notes"`
	UserID       uint       `json:"-"`
	BranchID     uint       `json:"-"`
}

type transferSide struct {
	kind     string
	acc      *database.TenantBankAccount
	currency string
}

func (s *CashBankService) resolveTransferSide(db *gorm.DB, ep TransferEndpoint, label string) (*transferSide, error) {
	switch ep.Kind {
	case TransferKindCash:
		return &transferSide{kind: TransferKindCash, currency: "PEN"}, nil
	case TransferKindBank:
		var acc database.TenantBankAccount
		if ep.AccountID == 0 || db.First(&acc, ep.AccountID).Error != nil {
			return nil, fmt.Errorf("la cuenta de %s no existe", label)
		}
		if !acc.Active {
			return nil, fmt.Errorf("la cuenta de %s (%s) está inactiva", label, acc.Name)
		}
		cur := strings.ToUpper(strings.TrimSpace(acc.Currency))
		if cur == "" {
			cur = "PEN"
		}
		if cur != "PEN" && cur != "USD" {
			return nil, fmt.Errorf("la cuenta %s está en %s: solo se admiten soles y dólares", acc.Name, cur)
		}
		return &transferSide{kind: TransferKindBank, acc: &acc, currency: cur}, nil
	default:
		return nil, fmt.Errorf("el %s debe ser la caja o una cuenta bancaria", label)
	}
}

func transferLabel(side *transferSide) string {
	if side.kind == TransferKindCash {
		return "Caja"
	}
	return side.acc.Name
}

// CreateTransfer registra la transferencia y sus dos movimientos. Devuelve la transferencia creada.
func (s *CashBankService) CreateTransfer(in TransferInput) (*database.TenantAccountTransfer, error) {
	amount := money.RoundDisplay(in.Amount)
	if amount <= 0 {
		return nil, errors.New("el monto a transferir debe ser mayor a cero")
	}
	if in.UserID == 0 {
		return nil, errors.New("usuario requerido")
	}
	if in.From.Kind == in.To.Kind && (in.From.Kind == TransferKindCash || in.From.AccountID == in.To.AccountID) {
		return nil, errors.New("el origen y el destino deben ser distintos")
	}
	date := time.Now()
	if in.Date != nil && !in.Date.IsZero() {
		date = *in.Date
	}

	var out *database.TenantAccountTransfer
	err := s.db.Transaction(func(tx *gorm.DB) error {
		from, err := s.resolveTransferSide(tx, in.From, "origen")
		if err != nil {
			return err
		}
		to, err := s.resolveTransferSide(tx, in.To, "destino")
		if err != nil {
			return err
		}

		// Importe que llega y TC aplicado.
		amountTo := amount
		var rate *float64
		if from.currency != to.currency {
			var r float64
			switch {
			case in.ExchangeRate != nil && *in.ExchangeRate > 0:
				r = *in.ExchangeRate
			default:
				v, _, ok := dailyfx.SaleRate(date.In(datespe.Location()).Format("2006-01-02"))
				if !ok || v <= 0 {
					return errors.New("las cuentas están en monedas distintas: indique el tipo de cambio de la operación")
				}
				r = v
			}
			rr := r
			rate = &rr
			if from.currency == "USD" { // USD → PEN
				amountTo = money.RoundDisplay(amount * r)
			} else { // PEN → USD
				amountTo = money.RoundDisplay(amount / r)
			}
			if amountTo <= 0 {
				return errors.New("el importe a recibir resulta cero: revise el monto y el tipo de cambio")
			}
		}

		// Caja: sesión abierta del usuario (el efectivo que sale/entra afecta a su arqueo).
		var sessionID *uint
		if from.kind == TransferKindCash || to.kind == TransferKindCash {
			sess, err := s.GetOpenSession(in.BranchID, in.UserID)
			if err != nil || sess == nil {
				return errors.New("para transferir desde o hacia la caja debe tener una sesión de caja abierta")
			}
			sid := sess.ID
			sessionID = &sid
			var lockRow database.TenantCashSession
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockRow, sid).Error; err != nil {
				return err
			}
			if from.kind == TransferKindCash && s.getExpectedBalance(sid) < amount-money.PaymentTolerance {
				return errors.New("la caja no tiene efectivo suficiente para esta transferencia")
			}
		}
		if from.kind == TransferKindBank {
			var cur database.TenantBankAccount
			// FOR UPDATE: dos transferencias simultáneas desde la misma cuenta se serializan y la segunda ve el saldo ya descontado.
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&cur, from.acc.ID).Error; err != nil {
				return err
			}
			if cur.Balance < amount-money.PaymentTolerance {
				return fmt.Errorf("la cuenta %s no tiene saldo suficiente (%.2f %s)", cur.Name, cur.Balance, from.currency)
			}
		}

		tr := &database.TenantAccountTransfer{
			FromKind: from.kind, ToKind: to.kind,
			AmountFrom: amount, CurrencyFrom: from.currency,
			AmountTo: amountTo, CurrencyTo: to.currency,
			ExchangeRate: rate, Date: date, Notes: strings.TrimSpace(in.Notes),
			UserID: in.UserID, CashSessionID: sessionID, Status: "active", CreatedAt: time.Now(),
		}
		if from.acc != nil {
			id := from.acc.ID
			tr.FromAccountID = &id
		}
		if to.acc != nil {
			id := to.acc.ID
			tr.ToAccountID = &id
		}
		if err := tx.Create(tr).Error; err != nil {
			return err
		}
		ref := fmt.Sprintf("TRF-%d", tr.ID)
		descOut := "Transferencia a " + transferLabel(to)
		descIn := "Transferencia desde " + transferLabel(from)
		if err := s.transferLeg(tx, tr, from, false, amount, rate, ref, descOut, in.UserID, sessionID, date, false); err != nil {
			return err
		}
		if err := s.transferLeg(tx, tr, to, true, amountTo, rate, ref, descIn, in.UserID, sessionID, date, false); err != nil {
			return err
		}
		out = tr
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// transferLeg crea UN movimiento de la transferencia (entrada o salida) y actualiza el saldo de la cuenta.
// isVoid marca un movimiento inverso (anulación de la transferencia).
func (s *CashBankService) transferLeg(tx *gorm.DB, tr *database.TenantAccountTransfer, side *transferSide, inbound bool, amount float64, rate *float64, ref, desc string, userID uint, sessionID *uint, date time.Time, isVoid bool) error {
	// En una anulación, el equivalente en soles y el TC del movimiento inverso se copian del movimiento original de esa cuenta.
	var origPEN, origRate *float64
	if isVoid && side.kind == TransferKindBank {
		var o database.TenantBankMovement
		if tx.Where("transfer_id = ? AND bank_account_id = ? AND category = ?", tr.ID, side.acc.ID, "Transferencia").First(&o).Error == nil {
			origPEN, origRate = o.AmountPEN, o.ExchangeRate
		}
	}
	trID := tr.ID
	cat := "Transferencia"
	if isVoid {
		cat = "Anulación transferencia"
	}
	if side.kind == TransferKindCash {
		typ := "expense"
		if inbound {
			typ = "income"
		}
		if sessionID == nil {
			return errors.New("sesión de caja requerida")
		}
		pen := amount
		return tx.Create(&database.TenantCashMovement{
			CashSessionID: *sessionID, Type: typ, Amount: amount, PaymentMethod: "cash", Category: cat, Reference: ref,
			Notes: desc, UserID: userID, TransferID: &trID,
			Currency: "PEN", AmountPEN: &pen, CreatedAt: time.Now(),
		}).Error
	}
	typ, delta := "debit", -amount
	if inbound {
		typ, delta = "credit", amount
	}
	// Equivalente en soles del movimiento (para reportes): PEN = el importe; USD = importe × TC de la operación.
	pen := amount
	var rateCopy *float64
	if side.currency == "USD" {
		if rate != nil {
			rr := *rate
			rateCopy = &rr
			pen = money.RoundDisplay(amount * rr)
		} else if r, _, ok := dailyfx.SaleRate(date.In(datespe.Location()).Format("2006-01-02")); ok && r > 0 {
			rr := r
			rateCopy = &rr
			pen = money.RoundDisplay(amount * r)
		}
	}
	if origPEN != nil {
		pen, rateCopy = *origPEN, origRate
	}
	acct := amount
	bm := database.TenantBankMovement{
		BankAccountID: side.acc.ID, Type: typ, Amount: amount, Description: desc, Reference: ref, Date: date, UserID: userID,
		CashSessionID: sessionID, Category: cat, TransferID: &trID,
		Currency: side.currency, ExchangeRate: rateCopy, AmountPEN: &pen, AccountAmount: &acct, CreatedAt: time.Now(),
	}
	if err := tx.Create(&bm).Error; err != nil {
		return err
	}
	return applyBankDelta(tx, side.acc.ID, &bm, delta, true)
}

// VoidTransfer anula una transferencia: crea los movimientos inversos (misma moneda e importes), devuelve los saldos y marca la
// transferencia como anulada. Requiere que el origen original pueda devolver lo recibido (saldo suficiente en la cuenta destino).
func (s *CashBankService) VoidTransfer(id, userID, branchID uint, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("indique el motivo de anulación")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var tr database.TenantAccountTransfer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&tr, id).Error; err != nil {
			return errors.New("transferencia no encontrada")
		}
		if tr.Status == "voided" {
			return errors.New("la transferencia ya fue anulada")
		}
		// Se marca como anulada ANTES de crear los movimientos inversos, con un UPDATE condicional: una segunda anulación simultánea
		// (doble clic, reintento) no encuentra la transferencia activa y no duplica los inversos.
		now0 := time.Now()
		res := tx.Model(&database.TenantAccountTransfer{}).Where("id = ? AND status = ?", id, "active").
			Updates(map[string]interface{}{"status": "voided", "voided_at": now0, "voided_by": userID, "void_reason": reason})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errors.New("la transferencia ya fue anulada")
		}
		from, err := s.resolveVoidSide(tx, tr.FromKind, tr.FromAccountID)
		if err != nil {
			return err
		}
		to, err := s.resolveVoidSide(tx, tr.ToKind, tr.ToAccountID)
		if err != nil {
			return err
		}
		var sessionID *uint
		if tr.FromKind == TransferKindCash || tr.ToKind == TransferKindCash {
			// La caja se devuelve en la sesión abierta del usuario que anula.
			sess, err := s.GetOpenSession(branchID, userID)
			if err != nil || sess == nil {
				return errors.New("para anular una transferencia que involucra la caja debe tener una sesión de caja abierta")
			}
			sid := sess.ID
			sessionID = &sid
			if tr.ToKind == TransferKindCash && s.getExpectedBalance(sid) < tr.AmountTo-money.PaymentTolerance {
				return errors.New("la caja no tiene efectivo suficiente para devolver esta transferencia")
			}
		}
		if tr.ToKind == TransferKindBank {
			var cur database.TenantBankAccount
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&cur, *tr.ToAccountID).Error; err == nil && cur.Balance < tr.AmountTo-money.PaymentTolerance {
				return fmt.Errorf("la cuenta %s no tiene saldo suficiente para devolver la transferencia", cur.Name)
			}
		}
		now := time.Now()
		ref := fmt.Sprintf("TRF-%d", tr.ID)
		// Inverso: el destino devuelve (sale) y el origen recibe (entra).
		if err := s.transferLeg(tx, &tr, to, false, tr.AmountTo, tr.ExchangeRate, ref, "Anulación de transferencia", userID, sessionID, now, true); err != nil {
			return err
		}
		if err := s.transferLeg(tx, &tr, from, true, tr.AmountFrom, tr.ExchangeRate, ref, "Anulación de transferencia", userID, sessionID, now, true); err != nil {
			return err
		}
		return nil
	})
}

func (s *CashBankService) resolveVoidSide(db *gorm.DB, kind string, accountID *uint) (*transferSide, error) {
	if kind == TransferKindCash {
		return &transferSide{kind: TransferKindCash, currency: "PEN"}, nil
	}
	if accountID == nil {
		return nil, errors.New("transferencia sin cuenta")
	}
	var acc database.TenantBankAccount
	if err := db.First(&acc, *accountID).Error; err != nil {
		return nil, errors.New("la cuenta de la transferencia ya no existe")
	}
	cur := strings.ToUpper(strings.TrimSpace(acc.Currency))
	if cur == "" {
		cur = "PEN"
	}
	return &transferSide{kind: TransferKindBank, acc: &acc, currency: cur}, nil
}

// ListTransfers devuelve las transferencias más recientes (máximo 200), con el nombre de origen y destino.
type TransferListRow struct {
	database.TenantAccountTransfer
	FromName string `json:"from_name"`
	ToName   string `json:"to_name"`
}

func (s *CashBankService) ListTransfers(limit int) ([]TransferListRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []database.TenantAccountTransfer
	if err := s.db.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	names := map[uint]string{}
	var accs []database.TenantBankAccount
	s.db.Unscoped().Find(&accs)
	for _, a := range accs {
		names[a.ID] = a.Name
	}
	out := make([]TransferListRow, 0, len(rows))
	for _, r := range rows {
		row := TransferListRow{TenantAccountTransfer: r, FromName: "Caja", ToName: "Caja"}
		if r.FromAccountID != nil {
			row.FromName = names[*r.FromAccountID]
		}
		if r.ToAccountID != nil {
			row.ToName = names[*r.ToAccountID]
		}
		out = append(out, row)
	}
	return out, nil
}
