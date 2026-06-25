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
	"tukifac/pkg/taxpayment"

	"gorm.io/gorm"
)

type ReceivableService struct {
	db *gorm.DB
}

func NewReceivableService(db *gorm.DB) *ReceivableService {
	return &ReceivableService{db: db}
}

type ListFilter struct {
	BranchID      uint
	ContactID     uint
	Status        string // open | overdue | bn_pending | all
	Search        string
	BnStatus      string
	Page          int
	PageSize      int
}

type ReceivableRow struct {
	SaleID                 uint       `json:"sale_id"`
	SaleNumber             string     `json:"sale_number"`
	DocType                string     `json:"doc_type"`
	IssueDate              time.Time  `json:"issue_date"`
	DueDate                *time.Time `json:"due_date,omitempty"`
	ContactID              uint       `json:"contact_id"`
	ContactName            string     `json:"contact_name"`
	ContactDocNumber       string     `json:"contact_doc_number"`
	Total                  float64    `json:"total"`
	Status                 string     `json:"status"`
	HasDetraccion          bool       `json:"has_detraccion"`
	DirectTarget           float64    `json:"direct_target"`
	DirectPaid             float64    `json:"direct_paid"`
	DirectDue              float64    `json:"direct_due"`
	SpotAmount             float64    `json:"spot_amount"`
	SpotPending            float64    `json:"spot_pending"`
	BnConfirmationStatus   string     `json:"bn_confirmation_status,omitempty"`
	BnConfirmationReference string    `json:"bn_confirmation_reference,omitempty"`
	IsOverdue              bool       `json:"is_overdue"`
}

type Summary struct {
	TotalDirectDue   float64 `json:"total_direct_due"`
	TotalSpotPending float64 `json:"total_spot_pending"`
	CountOpen        int64   `json:"count_open"`
	CountOverdue     int64   `json:"count_overdue"`
	CountBnPending   int64   `json:"count_bn_pending"`
}

type CollectPaymentInput struct {
	Payments      []salessvc.PaymentInput `json:"payments"`
	CashSessionID *uint                   `json:"cash_session_id,omitempty"`
	UserID        uint                    `json:"-"`
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
	Debit       float64   `json:"debit"`
	Credit      float64   `json:"credit"`
	Balance     float64   `json:"balance"`
	SaleID      uint      `json:"sale_id,omitempty"`
}

type StatementResult struct {
	ContactID   uint            `json:"contact_id"`
	ContactName string          `json:"contact_name"`
	Lines       []StatementLine `json:"lines"`
	TotalDue    float64         `json:"total_due"`
	SpotPending float64         `json:"spot_pending"`
}

func (s *ReceivableService) List(f ListFilter) ([]ReceivableRow, int64, error) {
	q := s.db.Model(&database.TenantSale{}).
		Where("tenant_sales.status != ?", "cancelled").
		Where("tenant_sales.doc_type IN ?", []string{"01", "03", "NV"})

	if f.BranchID > 0 {
		q = q.Where("tenant_sales.branch_id = ?", f.BranchID)
	}
	if f.ContactID > 0 {
		q = q.Where("tenant_sales.contact_id = ?", f.ContactID)
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

	ids := make([]uint, len(sales))
	contactIDs := make([]uint, 0, len(sales))
	for i := range sales {
		ids[i] = sales[i].ID
		if sales[i].ContactID != nil && *sales[i].ContactID > 0 {
			contactIDs = append(contactIDs, *sales[i].ContactID)
		}
	}

	detBySale, payBySale, contactsByID, err := s.loadRelated(ids, contactIDs)
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
		row := s.buildRow(sale, det, payments, contactsByID, now)
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
	out := &Summary{}
	for _, r := range rows {
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
		return fmt.Errorf("el cobro (S/ %.2f) supera el saldo directo (S/ %.2f)", money.RoundDisplay(newDirect), money.RoundDisplay(directDue))
	}

	cbSvc := cashbanksvc.NewCashBankService(s.db)
	payLines := make([]cashbanksvc.PaymentLineInput, 0, len(in.Payments))
	for _, p := range in.Payments {
		if p.Amount <= 0 {
			continue
		}
		payLines = append(payLines, cashbanksvc.PaymentLineInput{Method: p.Method, Amount: p.Amount})
	}
	cashSessionID, err := cbSvc.ResolveCashSessionForPayments(sale.BranchID, in.UserID, in.CashSessionID, payLines)
	if err != nil {
		return err
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, p := range in.Payments {
			if p.Amount <= 0 || p.Method == "" {
				continue
			}
			if err := tx.Create(&database.TenantSalePayment{
				SaleID: saleID,
				Method: p.Method,
				Amount: p.Amount,
			}).Error; err != nil {
				return err
			}
			desc := "Cobro " + sale.Number
			if err := cbSvc.RecordPayment(tx, p.Method, p.Amount, cashSessionID, sale.Number, desc, &sale.ID, in.UserID); err != nil {
				return err
			}
		}

		var all []database.TenantSalePayment
		if err := tx.Where("sale_id = ?", saleID).Find(&all).Error; err != nil {
			return err
		}
		_, paidAfter, dueAfter, _, _, _ := SaleBalance(sale, det, all)
		newStatus := sale.Status
		if dueAfter < money.PaymentTolerance {
			newStatus = "paid"
		} else {
			newStatus = "credit"
		}
		updates := map[string]interface{}{"status": newStatus}
		if paidAfter > 0 && newStatus == "paid" {
			updates["payment_method"] = salessvc.PrimaryDirectPaymentMethod(in.Payments, sale.PaymentMethod)
		}
		if cashSessionID != nil && *cashSessionID > 0 {
			updates["cash_session_id"] = *cashSessionID
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

	q := s.db.Where("contact_id = ? AND status != ?", contactID, "cancelled")
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

	ids := make([]uint, len(sales))
	for i := range sales {
		ids[i] = sales[i].ID
	}
	detBySale, payBySale, _, err := s.loadRelated(ids, nil)
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
		running += target
		res.Lines = append(res.Lines, StatementLine{
			Date:        sale.IssueDate,
			Type:        "invoice",
			Reference:   sale.Number,
			Description: "Documento " + sale.Number,
			Debit:       target,
			Balance:     money.RoundDisplay(running),
			SaleID:      sale.ID,
		})
		for _, p := range payments {
			if taxpayment.IsDetractionCode(p.Method) || paymentcondition.IsCreditCode(p.Method) {
				continue
			}
			running -= p.Amount
			res.Lines = append(res.Lines, StatementLine{
				Date:        p.CreatedAt,
				Type:        "payment",
				Reference:   p.Reference,
				Description: "Cobro " + sale.Number + " (" + p.Method + ")",
				Credit:      p.Amount,
				Balance:     money.RoundDisplay(running),
				SaleID:      sale.ID,
			})
		}
		if due > 0 {
			res.TotalDue += due
		}
		if spotPending > 0 {
			totalSpot += spotPending
		} else if spotAmt > 0 && det != nil && det.BnConfirmationStatus == BnStatusConfirmed {
			_ = spotAmt
		}
	}
	res.TotalDue = money.RoundDisplay(res.TotalDue)
	res.SpotPending = money.RoundDisplay(totalSpot)
	return res, nil
}

func (s *ReceivableService) loadRelated(
	saleIDs []uint,
	contactIDs []uint,
) (map[uint]*database.TenantSaleDetraccion, map[uint][]database.TenantSalePayment, map[uint]database.TenantContact, error) {
	detBySale := map[uint]*database.TenantSaleDetraccion{}
	var dets []database.TenantSaleDetraccion
	if err := s.db.Where("sale_id IN ?", saleIDs).Find(&dets).Error; err != nil {
		return nil, nil, nil, err
	}
	for i := range dets {
		d := dets[i]
		detBySale[d.SaleID] = &d
	}

	payBySale := map[uint][]database.TenantSalePayment{}
	var pays []database.TenantSalePayment
	if err := s.db.Where("sale_id IN ?", saleIDs).Order("created_at ASC").Find(&pays).Error; err != nil {
		return nil, nil, nil, err
	}
	for _, p := range pays {
		payBySale[p.SaleID] = append(payBySale[p.SaleID], p)
	}

	contactsByID := map[uint]database.TenantContact{}
	if len(contactIDs) > 0 {
		var contacts []database.TenantContact
		if err := s.db.Where("id IN ?", contactIDs).Find(&contacts).Error; err != nil {
			return nil, nil, nil, err
		}
		for _, c := range contacts {
			contactsByID[c.ID] = c
		}
	}
	return detBySale, payBySale, contactsByID, nil
}

func (s *ReceivableService) buildRow(
	sale database.TenantSale,
	det *database.TenantSaleDetraccion,
	payments []database.TenantSalePayment,
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
	isOverdue := false
	if sale.DueDate != nil && due > 0 {
		isOverdue = sale.DueDate.Before(now)
	}
	return ReceivableRow{
		SaleID:               sale.ID,
		SaleNumber:           sale.Number,
		DocType:              sale.DocType,
		IssueDate:            sale.IssueDate,
		DueDate:              sale.DueDate,
		ContactID:            contactIDVal(sale.ContactID),
		ContactName:          name,
		ContactDocNumber:     docNum,
		Total:                sale.Total,
		Status:               sale.Status,
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
		IsOverdue: isOverdue,
	}
}

func contactIDVal(id *uint) uint {
	if id == nil {
		return 0
	}
	return *id
}
