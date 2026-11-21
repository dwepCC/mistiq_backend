package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	cashbanksvc "tukifac/internal/cashbank/service"
	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/salescope"
	"tukifac/pkg/taxpayment"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ReceivableService struct {
	db *gorm.DB
}

func NewReceivableService(db *gorm.DB) *ReceivableService {
	return &ReceivableService{db: db}
}

type ListFilter struct {
	// SaleID una sola venta (para cobrar el saldo desde el pedido que la originó).
	SaleID        uint
	BranchID      uint
	ContactID     uint
	Status        string // open | overdue | bn_pending | all
	Search        string
	BnStatus      string
	Page          int
	PageSize      int
}

type ReceivableRow struct {
	SaleID           uint       `json:"sale_id"`
	SaleNumber       string     `json:"sale_number"`
	DocType          string     `json:"doc_type"`
	IssueDate        time.Time  `json:"issue_date"`
	DueDate          *time.Time `json:"due_date,omitempty"`
	ContactID        uint       `json:"contact_id"`
	ContactName      string     `json:"contact_name"`
	ContactDocNumber string     `json:"contact_doc_number"`
	Total            float64    `json:"total"`
	// Currency/ExchangeRate: moneda del comprobante (PEN | USD). Total, DirectTarget, DirectPaid y
	// DirectDue están en ESA moneda; la detracción SPOT siempre en soles (exige factura en PEN).
	Currency                string           `json:"currency"`
	ExchangeRate            *float64         `json:"exchange_rate,omitempty"`
	Status                  string           `json:"status"`
	PaymentConditionCode    string           `json:"payment_condition_code"`
	HasDetraccion           bool             `json:"has_detraccion"`
	DirectTarget            float64          `json:"direct_target"`
	DirectPaid              float64          `json:"direct_paid"`
	DirectDue               float64          `json:"direct_due"`
	SpotAmount              float64          `json:"spot_amount"`
	SpotPending             float64          `json:"spot_pending"`
	BnConfirmationStatus    string           `json:"bn_confirmation_status,omitempty"`
	BnConfirmationReference string           `json:"bn_confirmation_reference,omitempty"`
	IsOverdue               bool             `json:"is_overdue"`
	Installments            []InstallmentRow `json:"installments,omitempty"`
	InstallmentsPending     int              `json:"installments_pending"`
	NextInstallmentDue      *time.Time       `json:"next_installment_due,omitempty"`
}

// CurrencySummary es el saldo directo pendiente de una moneda: los saldos de monedas distintas
// no se suman entre sí (Summary.TotalDirectDue sí las mezcla; este es el desglose correcto).
type CurrencySummary struct {
	Currency     string  `json:"currency"`
	DirectDue    float64 `json:"direct_due"`
	CountOpen    int64   `json:"count_open"`
	CountOverdue int64   `json:"count_overdue"`
	// DirectDuePEN: este saldo convertido a soles con el TC de cada documento (en PEN = DirectDue).
	DirectDuePEN float64 `json:"direct_due_pen"`
}

type Summary struct {
	ByCurrency []CurrencySummary `json:"by_currency"`
	// TotalDirectDue suma los saldos de TODAS las monedas tal cual (legado): usar ByCurrency.
	TotalDirectDue float64 `json:"total_direct_due"`
	// TotalDirectDuePEN: saldo directo de todas las monedas EN SOLES (cada USD al TC de su documento). RateEstimatedCount:
	// documentos en USD sin TC guardado (convertidos con el TC del día de emisión); RateMissingCount: sin ningún TC (no incluidos).
	TotalDirectDuePEN  float64 `json:"total_direct_due_pen"`
	RateEstimatedCount int64   `json:"rate_estimated_count"`
	RateMissingCount   int64   `json:"rate_missing_count"`
	TotalSpotPending   float64 `json:"total_spot_pending"`
	CountOpen          int64   `json:"count_open"`
	CountOverdue       int64   `json:"count_overdue"`
	CountBnPending     int64   `json:"count_bn_pending"`
}

type CollectPaymentInput struct {
	Payments            []salessvc.PaymentInput `json:"payments"`
	CashSessionID       *uint                   `json:"cash_session_id,omitempty"`
	PreferInstallmentID uint                    `json:"prefer_installment_id,omitempty"`
	UserID              uint                    `json:"-"`
}

type ConfirmBNInput struct {
	Status    string `json:"status"`
	Reference string `json:"reference,omitempty"`
}

type StatementLine struct {
	Date        time.Time `json:"date"`
	Type        string    `json:"type"`
	Reference   string    `json:"reference"`
	Description string    `json:"description"`
	// Debit/Credit: importe en la moneda del documento (Currency). Balance: saldo corriente EN SOLES (cada USD al TC de su
	// documento); DebitPEN/CreditPEN: el importe de esta línea en soles. En un contacto solo en soles todo es igual que siempre.
	Debit        float64  `json:"debit"`
	Credit       float64  `json:"credit"`
	Currency     string   `json:"currency"`
	ExchangeRate *float64 `json:"exchange_rate,omitempty"`
	DebitPEN     float64  `json:"debit_pen"`
	CreditPEN    float64  `json:"credit_pen"`
	Balance      float64  `json:"balance"`
	SaleID       uint     `json:"sale_id,omitempty"`
	// CashSessionID: Caja donde OCURRIÓ el cobro (TenantSalePayment.CashSessionID) — solo en
	// líneas type="payment"; nil en "invoice" (esa línea es el registro del documento, cuya
	// propia Caja es TenantSale.CashSessionID, un dato distinto que este estado de cuenta no
	// muestra). Puramente informativo: no cambia ningún cálculo de saldo.
	CashSessionID *uint `json:"cash_session_id,omitempty"`
}

type StatementResult struct {
	ContactID   uint            `json:"contact_id"`
	ContactName string          `json:"contact_name"`
	Lines       []StatementLine `json:"lines"`
	// TotalDue: saldo directo EN SOLES (cada USD al TC de su documento); TotalDueByCurrency: el mismo saldo en la moneda de cada
	// documento. RateEstimatedCount / RateMissingCount: documentos en USD sin TC guardado.
	TotalDue           float64           `json:"total_due"`
	TotalDueByCurrency []CurrencySummary `json:"total_due_by_currency,omitempty"`
	RateEstimatedCount int64             `json:"rate_estimated_count"`
	RateMissingCount   int64             `json:"rate_missing_count"`
	SpotPending        float64           `json:"spot_pending"`
}

func (s *ReceivableService) List(f ListFilter) ([]ReceivableRow, int64, error) {
	// Ventas con saldo pendiente (crédito, parcial o SPOT BN). Doc types reales: BOLETA, FACTURA, NOTA DE VENTA, etc.
	q := s.db.Model(&database.TenantSale{}).
		Where("tenant_sales.status != ?", "cancelled").
		Where(salescope.NotaWithoutLiveIssuedWhere("tenant_sales"))

	if f.BranchID > 0 {
		q = q.Where("tenant_sales.branch_id = ?", f.BranchID)
	}
	if f.ContactID > 0 {
		q = q.Where("tenant_sales.contact_id = ?", f.ContactID)
	}
	if f.SaleID > 0 {
		q = q.Where("tenant_sales.id = ?", f.SaleID)
	}
	if f.Search != "" {
		like := "%" + strings.TrimSpace(f.Search) + "%"
		q = q.Joins("LEFT JOIN tenant_contacts c ON c.id = tenant_sales.contact_id").
			Where("tenant_sales.number LIKE ? OR c.business_name LIKE ? OR c.doc_number LIKE ?", like, like, like)
	}

	var sales []database.TenantSale
	if err := q.Order("tenant_sales.issue_date DESC, tenant_sales.id DESC").Find(&sales).Error; err != nil {
		return nil, 0, err
	}
	if len(sales) == 0 {
		return []ReceivableRow{}, 0, nil
	}
	if err := AttachNoteCredits(s.db, sales); err != nil {
		return nil, 0, err
	}

	ids := make([]uint, len(sales))
	contactIDs := make([]uint, 0, len(sales))
	for i := range sales {
		ids[i] = sales[i].ID
		if sales[i].ContactID != nil && *sales[i].ContactID > 0 {
			contactIDs = append(contactIDs, *sales[i].ContactID)
		}
	}

	detBySale, payBySale, instBySale, contactsByID, err := s.loadRelated(ids, contactIDs)
	if err != nil {
		return nil, 0, err
	}

	now := time.Now()
	rows := make([]ReceivableRow, 0, len(sales))
	for _, sale := range sales {
		det := detBySale[sale.ID]
		payments := payBySale[sale.ID]
		if !HasOpenReceivable(sale, det, payments) {
			continue
		}
		row := s.buildRow(sale, det, payments, instBySale[sale.ID], contactsByID, now)
		if !matchStatusFilter(f, row) {
			continue
		}
		if f.BnStatus != "" && row.BnConfirmationStatus != f.BnStatus {
			continue
		}
		rows = append(rows, row)
	}

	total := int64(len(rows))
	page := f.Page
	if page < 1 {
		page = 1
	}
	pageSize := f.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	start := (page - 1) * pageSize
	if start >= len(rows) {
		return []ReceivableRow{}, total, nil
	}
	end := start + pageSize
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], total, nil
}

func matchStatusFilter(f ListFilter, row ReceivableRow) bool {
	switch strings.TrimSpace(f.Status) {
	case "", "all", "open":
		return true
	case "overdue":
		return row.IsOverdue && row.DirectDue > 0
	case "bn_pending":
		return row.SpotPending > 0 && row.BnConfirmationStatus == BnStatusPending
	default:
		return true
	}
}

func (s *ReceivableService) Summary(branchID uint) (*Summary, error) {
	rows, _, err := s.List(ListFilter{BranchID: branchID, Status: "open", Page: 1, PageSize: 100000})
	if err != nil {
		return nil, err
	}
	out := &Summary{ByCurrency: []CurrencySummary{}}
	byCur := map[string]*CurrencySummary{}
	var order []string
	conv := newPENConverter()
	for _, r := range rows {
		cs := byCur[r.Currency]
		if cs == nil {
			cs = &CurrencySummary{Currency: r.Currency}
			byCur[r.Currency] = cs
			order = append(order, r.Currency)
		}
		cs.DirectDue += r.DirectDue
		if f, est, ok := conv.factor(r.Currency, r.ExchangeRate, r.IssueDate); ok {
			cs.DirectDuePEN += r.DirectDue * f
			if est && r.DirectDue > 0 {
				out.RateEstimatedCount++
			}
		} else if r.DirectDue > 0 {
			out.RateMissingCount++
		}
		cs.CountOpen++
		if r.IsOverdue && r.DirectDue > 0 {
			cs.CountOverdue++
		}
		out.TotalDirectDue += r.DirectDue
		out.TotalSpotPending += r.SpotPending
		out.CountOpen++
		if r.IsOverdue && r.DirectDue > 0 {
			out.CountOverdue++
		}
		if r.SpotPending > 0 && r.BnConfirmationStatus == BnStatusPending {
			out.CountBnPending++
		}
	}
	for _, c := range order {
		cs := byCur[c]
		cs.DirectDue = money.RoundDisplay(cs.DirectDue)
		cs.DirectDuePEN = money.RoundDisplay(cs.DirectDuePEN)
		out.TotalDirectDuePEN += cs.DirectDuePEN
		out.ByCurrency = append(out.ByCurrency, *cs)
	}
	out.TotalDirectDuePEN = money.RoundDisplay(out.TotalDirectDuePEN)
	out.TotalDirectDue = money.RoundDisplay(out.TotalDirectDue)
	out.TotalSpotPending = money.RoundDisplay(out.TotalSpotPending)
	return out, nil
}

func (s *ReceivableService) Collect(saleID uint, in CollectPaymentInput) error {
	if len(in.Payments) == 0 {
		return errors.New("debe indicar al menos un pago")
	}
	var sale database.TenantSale
	if err := s.db.First(&sale, saleID).Error; err != nil {
		return errors.New("venta no encontrada")
	}
	if sale.Status == "cancelled" {
		return errors.New("no se puede cobrar una venta anulada")
	}
	if salescope.IsNoteDocType(sale.DocType) {
		return errors.New("una nota de crédito/débito no es una venta cobrable")
	}
	// Las notas de crédito aceptadas bajan lo que se puede cobrar de esta venta.
	one := []database.TenantSale{sale}
	if err := AttachNoteCredits(s.db, one); err != nil {
		return err
	}
	sale = one[0]
	var liveChild int64
	if err := s.db.Model(&database.TenantSale{}).
		Where("issued_from_nota_sale_id = ? AND status <> ?", saleID, "cancelled").
		Count(&liveChild).Error; err != nil {
		return err
	}
	if liveChild > 0 {
		return errors.New("esta nota de venta ya fue convertida en factura/boleta; cobre el saldo desde el comprobante electrónico")
	}

	var det *database.TenantSaleDetraccion
	var detRow database.TenantSaleDetraccion
	if err := s.db.First(&detRow, "sale_id = ?", saleID).Error; err == nil {
		det = &detRow
	}

	var existing []database.TenantSalePayment
	if err := s.db.Where("sale_id = ?", saleID).Find(&existing).Error; err != nil {
		return err
	}
	directTarget, directPaid, directDue, _, _, _ := SaleBalance(sale, det, existing)
	if directDue < money.PaymentTolerance {
		return errors.New("esta venta no tiene saldo directo pendiente")
	}

	var newDirect float64
	for _, p := range in.Payments {
		if p.Amount <= 0 || p.Method == "" {
			continue
		}
		if taxpayment.IsDetractionCode(p.Method) || paymentcondition.IsCreditCode(p.Method) {
			return errors.New("use métodos de pago directos (efectivo, banco, etc.) para cobrar")
		}
		newDirect += p.Amount
	}
	if newDirect <= 0 {
		return errors.New("el monto a cobrar debe ser mayor a cero")
	}
	if money.RoundDisplay(directPaid+newDirect) > money.RoundDisplay(directTarget)+money.PaymentTolerance {
		sym := "S/"
		if strings.EqualFold(strings.TrimSpace(sale.Currency), "USD") {
			sym = "US$"
		}
		return fmt.Errorf("el cobro (%s %.2f) supera el saldo directo (%s %.2f)", sym, money.RoundDisplay(newDirect), sym, money.RoundDisplay(directDue))
	}

	cbSvc := cashbanksvc.NewCashBankService(s.db)
	refLines := make([]cashbanksvc.PaymentRefLine, 0, len(in.Payments))
	for _, p := range in.Payments {
		if p.Amount > 0 && p.Method != "" {
			refLines = append(refLines, cashbanksvc.PaymentRefLine{Method: p.Method, Reference: p.Reference})
		}
	}
	if err := cbSvc.ValidatePaymentReferences(refLines); err != nil {
		return err
	}
	payLines := make([]cashbanksvc.PaymentLineInput, 0, len(in.Payments))
	for _, p := range in.Payments {
		if p.Amount <= 0 {
			continue
		}
		payLines = append(payLines, cashbanksvc.PaymentLineInput{Method: p.Method, Amount: p.Amount})
	}
	// ResolveCashSessionForCollection exige sesión de caja del usuario para CUALQUIER cobro,
	// sin importar el método (antes: ResolveCashSessionForPayments, que solo la exigía para
	// efectivo — un cobro 100% Yape/Plin/transferencia/tarjeta podía registrarse sin sesión).
	cashSessionID, err := cbSvc.ResolveCashSessionForCollection(sale.BranchID, in.UserID, in.CashSessionID, payLines)
	if err != nil {
		return err
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		// Bloqueo de la venta y nueva lectura del saldo DENTRO de la transacción: la validación de arriba se hizo sin bloqueo, así que
		// dos cobros simultáneos (doble clic) podían pasar ambos y superar el saldo. El segundo espera aquí y ve el saldo ya cobrado.
		var locked database.TenantSale
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, saleID).Error; err != nil {
			return errors.New("venta no encontrada")
		}
		if locked.Status == "cancelled" {
			return errors.New("no se puede cobrar una venta anulada")
		}
		var fresh []database.TenantSalePayment
		if err := tx.Where("sale_id = ?", saleID).Find(&fresh).Error; err != nil {
			return err
		}
		freshTarget, freshPaid, freshDue, _, _, _ := SaleBalance(sale, det, fresh)
		if freshDue < money.PaymentTolerance {
			return errors.New("esta venta no tiene saldo directo pendiente")
		}
		if money.RoundDisplay(freshPaid+newDirect) > money.RoundDisplay(freshTarget)+money.PaymentTolerance {
			sym := "S/"
			if strings.EqualFold(strings.TrimSpace(sale.Currency), "USD") {
				sym = "US$"
			}
			return fmt.Errorf("el cobro (%s %.2f) supera el saldo directo (%s %.2f)", sym, money.RoundDisplay(newDirect), sym, money.RoundDisplay(freshDue))
		}
		var collected float64
		for _, p := range in.Payments {
			if p.Amount <= 0 || p.Method == "" {
				continue
			}
			// CashSessionID aquí es la Caja donde OCURRIÓ este cobro — puede ser distinta de
			// sale.CashSessionID (la Caja donde se REGISTRÓ la venta), que nunca se toca en este
			// flujo. Ver comentario del campo en pkg/database/migrations.go.
			if err := tx.Create(&database.TenantSalePayment{
				SaleID:        saleID,
				Method:        p.Method,
				Amount:        p.Amount,
				Reference:     strings.TrimSpace(p.Reference),
				CashSessionID: cashSessionID,
			}).Error; err != nil {
				return err
			}
			desc := "Cobro " + sale.Number
			if err := cbSvc.RecordPaymentToDestination(tx, p.Method, p.Amount, cashSessionID, sale.Number, desc, &sale.ID, in.UserID, p.DestinationAccountID); err != nil {
				return err
			}
			collected += p.Amount
		}

		if err := applyPaymentToInstallmentsTx(tx, saleID, collected, in.PreferInstallmentID); err != nil {
			return err
		}

		var all []database.TenantSalePayment
		if err := tx.Where("sale_id = ?", saleID).Find(&all).Error; err != nil {
			return err
		}
		_, paidAfter, dueAfter, _, _, _ := SaleBalance(sale, det, all)
		// Venta a crédito: se marca pagada solo cuando el saldo directo queda en cero.
		newStatus := sale.Status
		if dueAfter < money.PaymentTolerance {
			newStatus = "paid"
			// Cierra cuotas pendientes por redondeo o ventas sin cuotas parciales.
			if err := tx.Model(&database.TenantSaleCreditInstallment{}).
				Where("sale_id = ? AND status != ?", saleID, InstallmentPaid).
				Updates(map[string]interface{}{
					"status":      InstallmentPaid,
					"paid_amount": gorm.Expr("amount"),
				}).Error; err != nil {
				return err
			}
		} else {
			newStatus = "credit"
		}
		// sale.CashSessionID NUNCA se toca aquí: representa exclusivamente dónde se REGISTRÓ la
		// venta, no dónde se cobró. Cada cobro conserva su propia Caja en
		// TenantSalePayment.CashSessionID (arriba) y en el TenantCashMovement/TenantBankMovement
		// que crea RecordPayment — eso es suficiente para saber "dónde ocurrió cada pago" sin
		// pisar la sesión de registro de la venta.
		updates := map[string]interface{}{"status": newStatus}
		if paidAfter > 0 && newStatus == "paid" {
			updates["payment_method"] = salessvc.PrimaryDirectPaymentMethod(in.Payments, sale.PaymentMethod)
		}
		return tx.Model(&sale).Updates(updates).Error
	})
}

func (s *ReceivableService) ConfirmBN(saleID uint, in ConfirmBNInput) (*database.TenantSaleDetraccion, error) {
	status := strings.TrimSpace(strings.ToLower(in.Status))
	if status != BnStatusConfirmed && status != BnStatusRejected {
		return nil, errors.New("status debe ser confirmed o rejected")
	}
	var det database.TenantSaleDetraccion
	if err := s.db.First(&det, "sale_id = ?", saleID).Error; err != nil {
		return nil, errors.New("la venta no tiene detracción registrada")
	}
	if taxpayment.IsSelfDetractionStatus(det.BnConfirmationStatus) {
		return nil, errors.New("esta venta es de autodetracción: el cliente no deposita en el BN, por lo que no hay acreditación que confirmar")
	}
	if det.BnConfirmationStatus == BnStatusConfirmed {
		return nil, errors.New("la detracción BN ya fue confirmada")
	}
	now := time.Now()
	det.BnConfirmationStatus = status
	det.BnConfirmedAt = &now
	det.BnConfirmationReference = strings.TrimSpace(in.Reference)
	det.UpdatedAt = now
	if err := s.db.Save(&det).Error; err != nil {
		return nil, err
	}
	return &det, nil
}

func (s *ReceivableService) Statement(contactID uint, branchID uint) (*StatementResult, error) {
	if contactID == 0 {
		return nil, errors.New("contact_id requerido")
	}
	var contact database.TenantContact
	if err := s.db.First(&contact, contactID).Error; err != nil {
		return nil, errors.New("contacto no encontrado")
	}

	q := s.db.Where("contact_id = ? AND status != ?", contactID, "cancelled").
		Where(salescope.NotaWithoutLiveIssuedWhere("tenant_sales"))
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}
	var sales []database.TenantSale
	if err := q.Order("issue_date ASC, id ASC").Find(&sales).Error; err != nil {
		return nil, err
	}
	if len(sales) == 0 {
		name := contact.BusinessName
		if name == "" {
			name = contact.TradeName
		}
		return &StatementResult{ContactID: contactID, ContactName: name, Lines: []StatementLine{}}, nil
	}

	if err := AttachNoteCredits(s.db, sales); err != nil {
		return nil, err
	}
	ids := make([]uint, len(sales))
	for i := range sales {
		ids[i] = sales[i].ID
	}
	detBySale, payBySale, _, _, err := s.loadRelated(ids, nil)
	if err != nil {
		return nil, err
	}

	name := contact.BusinessName
	if name == "" {
		name = contact.TradeName
	}
	res := &StatementResult{ContactID: contactID, ContactName: name, Lines: []StatementLine{}}
	var running float64
	var totalSpot float64
	conv := newPENConverter()
	dueByCur := map[string]*CurrencySummary{}
	var dueOrder []string

	for _, sale := range sales {
		det := detBySale[sale.ID]
		payments := payBySale[sale.ID]
		if !HasOpenReceivable(sale, det, payments) && sale.Status != "credit" && sale.Status != "paid" {
			// incluir ventas con movimiento histórico
		}
		target, paid, due, spotAmt, spotPending, _ := SaleBalance(sale, det, payments)
		if target <= 0 && paid <= 0 {
			continue
		}
		cur := strings.ToUpper(strings.TrimSpace(sale.Currency))
		if cur == "" {
			cur = "PEN"
		}
		factor, est, ok := conv.factor(cur, sale.ExchangeRate, sale.IssueDate)
		if !ok {
			factor = 0 // USD sin ningún TC: el documento se lista pero no entra en el saldo en soles
			res.RateMissingCount++
		} else if est {
			res.RateEstimatedCount++
		}
		running += target * factor
		res.Lines = append(res.Lines, StatementLine{
			Date:         sale.IssueDate,
			Type:         "invoice",
			Reference:    sale.Number,
			Description:  "Documento " + sale.Number,
			Debit:        target,
			Currency:     cur,
			ExchangeRate: sale.ExchangeRate,
			DebitPEN:     money.RoundDisplay(target * factor),
			Balance:      money.RoundDisplay(running),
			SaleID:       sale.ID,
		})
		for _, p := range payments {
			if taxpayment.IsDetractionCode(p.Method) || paymentcondition.IsCreditCode(p.Method) {
				continue
			}
			running -= p.Amount * factor
			res.Lines = append(res.Lines, StatementLine{
				Date:          p.CreatedAt,
				Type:          "payment",
				Reference:     p.Reference,
				Description:   "Cobro " + sale.Number + " (" + p.Method + ")",
				Credit:        p.Amount,
				Currency:      cur,
				ExchangeRate:  sale.ExchangeRate,
				CreditPEN:     money.RoundDisplay(p.Amount * factor),
				Balance:       money.RoundDisplay(running),
				SaleID:        sale.ID,
				CashSessionID: p.CashSessionID,
			})
		}
		if due > 0 {
			res.TotalDue += due * factor
			cs := dueByCur[cur]
			if cs == nil {
				cs = &CurrencySummary{Currency: cur}
				dueByCur[cur] = cs
				dueOrder = append(dueOrder, cur)
			}
			cs.DirectDue += due
			cs.DirectDuePEN += due * factor
			cs.CountOpen++
		}
		if spotPending > 0 {
			totalSpot += spotPending
		} else if spotAmt > 0 && det != nil && det.BnConfirmationStatus == BnStatusConfirmed {
			_ = spotAmt
		}
	}
	res.TotalDue = money.RoundDisplay(res.TotalDue)
	for _, c := range dueOrder {
		cs := dueByCur[c]
		cs.DirectDue = money.RoundDisplay(cs.DirectDue)
		cs.DirectDuePEN = money.RoundDisplay(cs.DirectDuePEN)
		res.TotalDueByCurrency = append(res.TotalDueByCurrency, *cs)
	}
	res.SpotPending = money.RoundDisplay(totalSpot)
	return res, nil
}

func (s *ReceivableService) loadRelated(
	saleIDs []uint,
	contactIDs []uint,
) (
	map[uint]*database.TenantSaleDetraccion,
	map[uint][]database.TenantSalePayment,
	map[uint][]database.TenantSaleCreditInstallment,
	map[uint]database.TenantContact,
	error,
) {
	detBySale := map[uint]*database.TenantSaleDetraccion{}
	var dets []database.TenantSaleDetraccion
	if err := s.db.Where("sale_id IN ?", saleIDs).Find(&dets).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	for i := range dets {
		d := dets[i]
		detBySale[d.SaleID] = &d
	}

	payBySale := map[uint][]database.TenantSalePayment{}
	var pays []database.TenantSalePayment
	if err := s.db.Where("sale_id IN ?", saleIDs).Order("created_at ASC").Find(&pays).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	for _, p := range pays {
		payBySale[p.SaleID] = append(payBySale[p.SaleID], p)
	}

	instBySale := map[uint][]database.TenantSaleCreditInstallment{}
	var insts []database.TenantSaleCreditInstallment
	if err := s.db.Where("sale_id IN ?", saleIDs).Order("installment_no ASC").Find(&insts).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	for _, inst := range insts {
		instBySale[inst.SaleID] = append(instBySale[inst.SaleID], inst)
	}

	contactsByID := map[uint]database.TenantContact{}
	if len(contactIDs) > 0 {
		var contacts []database.TenantContact
		if err := s.db.Where("id IN ?", contactIDs).Find(&contacts).Error; err != nil {
			return nil, nil, nil, nil, err
		}
		for _, c := range contacts {
			contactsByID[c.ID] = c
		}
	}
	return detBySale, payBySale, instBySale, contactsByID, nil
}

func (s *ReceivableService) buildRow(
	sale database.TenantSale,
	det *database.TenantSaleDetraccion,
	payments []database.TenantSalePayment,
	installments []database.TenantSaleCreditInstallment,
	contacts map[uint]database.TenantContact,
	now time.Time,
) ReceivableRow {
	target, paid, due, spotAmt, spotPending, bnStatus := SaleBalance(sale, det, payments)
	name := ""
	docNum := ""
	if sale.ContactID != nil {
		if c, ok := contacts[*sale.ContactID]; ok {
			name = c.BusinessName
			if name == "" {
				name = c.TradeName
			}
			docNum = c.DocNumber
		}
	}
	instRows := toInstallmentRows(installments, now)
	nextDue := nextOpenInstallmentDue(instRows)
	displayDue := sale.DueDate
	if nextDue != nil {
		displayDue = nextDue
	}
	isOverdue := false
	if due > 0 {
		if nextDue != nil {
			isOverdue = nextDue.Before(now)
		} else if sale.DueDate != nil {
			isOverdue = sale.DueDate.Before(now)
		}
		for _, ir := range instRows {
			if ir.IsOverdue {
				isOverdue = true
				break
			}
		}
	}
	payCond := sale.PaymentConditionCode
	if payCond == "" && (sale.Status == "credit" || len(instRows) > 0) {
		payCond = paymentcondition.CodeCredit
	}
	if payCond == "" {
		payCond = paymentcondition.CodeCash
	}
	return ReceivableRow{
		SaleID:               sale.ID,
		SaleNumber:           sale.Number,
		DocType:              sale.DocType,
		IssueDate:            sale.IssueDate,
		DueDate:              displayDue,
		ContactID:            contactIDVal(sale.ContactID),
		ContactName:          name,
		ContactDocNumber:     docNum,
		Total:                sale.Total,
		Currency:             saleCurrencyOrPEN(sale.Currency),
		ExchangeRate:         sale.ExchangeRate,
		Status:               sale.Status,
		PaymentConditionCode: payCond,
		HasDetraccion:        det != nil,
		DirectTarget:         target,
		DirectPaid:           paid,
		DirectDue:            due,
		SpotAmount:           spotAmt,
		SpotPending:          spotPending,
		BnConfirmationStatus: bnStatus,
		BnConfirmationReference: func() string {
			if det != nil {
				return det.BnConfirmationReference
			}
			return ""
		}(),
		IsOverdue:           isOverdue,
		Installments:        instRows,
		InstallmentsPending: countPendingInstallments(instRows),
		NextInstallmentDue:  nextDue,
	}
}

func saleCurrencyOrPEN(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	if c == "" {
		return "PEN"
	}
	return c
}

func contactIDVal(id *uint) uint {
	if id == nil {
		return 0
	}
	return *id
}
