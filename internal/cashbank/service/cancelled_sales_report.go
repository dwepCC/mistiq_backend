package service

import (
	"fmt"
	"strings"
	"time"

	"tukifac/internal/sales/nvdisplay"
	"tukifac/pkg/database"
	"tukifac/pkg/salescope"
)

func cancelledSaleRowKey(saleID uint, method string, amount float64) string {
	return fmt.Sprintf("%d:%s:%.2f", saleID, normalizeReportMethod(method), amount)
}

func cancelledSaleReasonFromNotes(notes string) string {
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return ""
	}
	const prefix = "ANULADA:"
	if idx := strings.Index(strings.ToUpper(notes), prefix); idx >= 0 {
		return strings.TrimSpace(notes[idx+len(prefix):])
	}
	return notes
}

func voidMovementRowKeys(voidMovements []database.TenantCashMovement) map[string]struct{} {
	seen := make(map[string]struct{}, len(voidMovements))
	for _, m := range voidMovements {
		if m.SaleID == nil {
			continue
		}
		seen[cancelledSaleRowKey(*m.SaleID, m.PaymentMethod, m.Amount)] = struct{}{}
	}
	return seen
}

func (s *CashBankService) listOrphanCancelledSalesForSession(session *database.TenantCashSession) ([]database.TenantSale, error) {
	if session == nil || session.ID == 0 {
		return nil, nil
	}
	q := salescope.CommercialSales(s.db.Model(&database.TenantSale{})).
		Where("(cash_session_id IS NULL OR cash_session_id = 0)").
		Where("user_id = ? AND branch_id = ?", sessionOwnerID(session), session.BranchID).
		Where("updated_at >= ?", session.OpenedAt).
		Where("status = ?", "cancelled")
	if session.ClosedAt != nil {
		q = q.Where("updated_at <= ?", *session.ClosedAt)
	}
	var sales []database.TenantSale
	err := q.Order("updated_at ASC").Find(&sales).Error
	return sales, err
}

func (s *CashBankService) listCancelledSalesForSession(session *database.TenantCashSession) ([]database.TenantSale, error) {
	if session == nil || session.ID == 0 {
		return nil, nil
	}
	var direct []database.TenantSale
	if err := s.db.Where("cash_session_id = ? AND status = ?", session.ID, "cancelled").
		Order("updated_at ASC").Find(&direct).Error; err != nil {
		return nil, err
	}
	orphans, err := s.listOrphanCancelledSalesForSession(session)
	if err != nil {
		return nil, err
	}
	seen := make(map[uint]struct{}, len(direct)+len(orphans))
	out := make([]database.TenantSale, 0, len(direct)+len(orphans))
	for _, sale := range append(direct, orphans...) {
		if _, ok := seen[sale.ID]; ok {
			continue
		}
		seen[sale.ID] = struct{}{}
		out = append(out, sale)
	}
	return out, nil
}

func (s *CashBankService) appendCancelledSalesFromPayments(
	report *SessionReport,
	session *database.TenantCashSession,
	voidMovements []database.TenantCashMovement,
) error {
	cancelledSales, err := s.listCancelledSalesForSession(session)
	if err != nil {
		return err
	}
	if len(cancelledSales) == 0 {
		return nil
	}

	saleIDs := make([]uint, 0, len(cancelledSales))
	salesByID := make(map[uint]database.TenantSale, len(cancelledSales))
	for _, sale := range cancelledSales {
		saleIDs = append(saleIDs, sale.ID)
		salesByID[sale.ID] = sale
	}
	displayDocNumbers := nvdisplay.LoadDisplayNumbersBySaleID(s.db, saleIDs)

	var payments []database.TenantSalePayment
	if err := s.db.Where("sale_id IN ?", saleIDs).Order("created_at ASC").Find(&payments).Error; err != nil {
		return err
	}

	seen := voidMovementRowKeys(voidMovements)
	reasonBySale := make(map[uint]string, len(cancelledSales))
	cancelledAtBySale := make(map[uint]time.Time, len(cancelledSales))
	for _, sale := range cancelledSales {
		reasonBySale[sale.ID] = cancelledSaleReasonFromNotes(sale.Notes)
		cancelledAtBySale[sale.ID] = sale.UpdatedAt
	}

	appendPaymentRow := func(sale database.TenantSale, meth string, amount float64, cancelledAt time.Time) {
		if amount <= 0 || IsDetractionPaymentMethod(meth) {
			return
		}
		key := cancelledSaleRowKey(sale.ID, meth, amount)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		docNumber := displayDocNumbers[sale.ID]
		if docNumber == "" {
			docNumber = sale.Number
		}
		report.CancelledSalesDetail = append(report.CancelledSalesDetail, CancelledSaleRow{
			Date:          cancelledAt,
			DocNumber:     docNumber,
			Amount:        amount,
			PaymentMethod: meth,
			Reason:        reasonBySale[sale.ID],
		})
	}

	paidSaleIDs := make(map[uint]struct{}, len(payments))
	for _, p := range payments {
		sale, ok := salesByID[p.SaleID]
		if !ok {
			continue
		}
		paidSaleIDs[sale.ID] = struct{}{}
		appendPaymentRow(sale, normalizeReportMethod(p.Method), p.Amount, cancelledAtBySale[sale.ID])
	}
	for _, sale := range cancelledSales {
		if _, ok := paidSaleIDs[sale.ID]; ok {
			continue
		}
		if sale.Total <= 0 {
			continue
		}
		appendPaymentRow(sale, normalizeReportMethod(sale.PaymentMethod), sale.Total, cancelledAtBySale[sale.ID])
	}
	return nil
}

func cancelledElectronicMovementID(paymentID uint) uint {
	return 3_000_000_000 + paymentID
}

func (s *CashBankService) buildCancelledElectronicMovementRows(f MovementReportFilters) ([]MovementReportRow, error) {
	if f.MovementType == "income" {
		return nil, nil
	}

	q := s.db.Table("tenant_sale_payments").
		Select(`tenant_sale_payments.id AS payment_id, tenant_sale_payments.sale_id, tenant_sale_payments.method,
			tenant_sale_payments.amount, tenant_sale_payments.reference, tenant_sale_payments.notes, tenant_sale_payments.created_at,
			tenant_sales.number AS sale_number, tenant_sales.user_id AS sale_user_id, tenant_sales.contact_id,
			tenant_sales.cash_session_id, tenant_sales.payment_method AS sale_payment_method, tenant_sales.updated_at AS sale_created_at,
			tenant_sales.notes AS sale_notes, tenant_cash_sessions.branch_id AS branch_id`).
		Joins("JOIN tenant_sales ON tenant_sales.id = tenant_sale_payments.sale_id").
		Joins("LEFT JOIN tenant_cash_sessions ON tenant_cash_sessions.id = tenant_sales.cash_session_id").
		Where("tenant_sales.status = ?", "cancelled").
		Scopes(salescope.ScopeCommercial("tenant_sales"))

	if f.SessionID > 0 {
		q = q.Where("tenant_sales.cash_session_id = ?", f.SessionID)
	}
	if f.BranchID > 0 {
		q = q.Where("tenant_cash_sessions.branch_id = ?", f.BranchID)
	}
	if f.UserID > 0 {
		q = q.Where("tenant_sales.user_id = ?", f.UserID)
	}
	if f.DateFrom != nil {
		q = q.Where("tenant_sales.updated_at >= ?", f.DateFrom)
	}
	if f.DateTo != nil {
		q = q.Where("tenant_sales.updated_at <= ?", f.DateTo)
	}
	if f.PaymentMethod != "" {
		q = applyPaymentMethodFilter(q, "tenant_sale_payments.method", f.PaymentMethod)
	}

	var payRows []salePayRow
	if err := q.Order("tenant_sales.updated_at DESC").Scan(&payRows).Error; err != nil {
		return nil, err
	}

	if f.SessionID > 0 {
		orphanRows, err := s.scanOrphanCancelledSalePaymentRows(f)
		if err != nil {
			return nil, err
		}
		payRows = append(payRows, orphanRows...)
	}

	if len(payRows) == 0 {
		return nil, nil
	}

	saleIDs := make([]uint, 0, len(payRows))
	contactIDs := make(map[uint]struct{})
	userIDs := make(map[uint]struct{})
	for _, p := range payRows {
		saleIDs = append(saleIDs, p.SaleID)
		userIDs[p.SaleUserID] = struct{}{}
		if p.ContactID != nil {
			contactIDs[*p.ContactID] = struct{}{}
		}
	}
	displayDocNumbers := nvdisplay.LoadDisplayNumbersBySaleID(s.db, saleIDs)
	contacts := loadContactNamesMap(s.db, contactIDs)
	users := loadUserNamesMap(s.db, userIDs)

	branchIDs := make(map[uint]struct{})
	for _, p := range payRows {
		if p.BranchID > 0 {
			branchIDs[p.BranchID] = struct{}{}
		}
	}
	branches := loadBranchNamesMap(s.db, branchIDs)

	rows := make([]MovementReportRow, 0, len(payRows))
	for _, p := range payRows {
		meth := normalizeReportMethod(p.Method)
		if IsDetractionPaymentMethod(meth) || IsCashPaymentMethod(meth) {
			continue
		}
		if f.PaymentMethod != "" && !paymentMethodMatchesFilter(meth, f.PaymentMethod) {
			continue
		}
		contactName := ""
		if p.ContactID != nil {
			contactName = contacts[*p.ContactID]
		}
		docNumber := displayDocNumbers[p.SaleID]
		if docNumber == "" {
			docNumber = p.SaleNumber
		}
		rows = append(rows, MovementReportRow{
			Date:          p.SaleCreatedAt,
			Type:          "anulacion_venta",
			DocNumber:     docNumber,
			ContactName:   contactName,
			UserName:      users[p.SaleUserID],
			BranchName:    branches[p.BranchID],
			PaymentMethod: meth,
			Amount:        -p.Amount,
			MovementID:    cancelledElectronicMovementID(p.PaymentID),
			CashSessionID: p.CashSessionID,
			Category:      "Anulación venta",
			CashReference: p.Reference,
			NotesDetail:   cancelledSaleReasonFromNotes(p.SaleNotes),
		})
	}
	return rows, nil
}

func paymentMethodMatchesFilter(method, filter string) bool {
	method = normalizeReportMethod(method)
	for _, v := range paymentMethodVariantsLower(filter) {
		if normalizeReportMethod(v) == method {
			return true
		}
	}
	return false
}

func (s *CashBankService) scanOrphanCancelledSalePaymentRows(f MovementReportFilters) ([]salePayRow, error) {
	var session database.TenantCashSession
	if err := s.db.First(&session, f.SessionID).Error; err != nil {
		return nil, nil
	}
	q := s.db.Table("tenant_sale_payments").
		Select(`tenant_sale_payments.id AS payment_id, tenant_sale_payments.sale_id, tenant_sale_payments.method,
			tenant_sale_payments.amount, tenant_sale_payments.reference, tenant_sale_payments.notes, tenant_sale_payments.created_at,
			tenant_sales.number AS sale_number, tenant_sales.user_id AS sale_user_id, tenant_sales.contact_id,
			? AS cash_session_id, tenant_sales.payment_method AS sale_payment_method, tenant_sales.updated_at AS sale_created_at,
			tenant_sales.notes AS sale_notes, ? AS branch_id`, f.SessionID, session.BranchID).
		Joins("JOIN tenant_sales ON tenant_sales.id = tenant_sale_payments.sale_id").
		Where("(tenant_sales.cash_session_id IS NULL OR tenant_sales.cash_session_id = 0)").
		Scopes(salescope.ScopeCommercial("tenant_sales")).
		Where("tenant_sales.user_id = ? AND tenant_sales.branch_id = ?", sessionOwnerID(&session), session.BranchID).
		Where("tenant_sales.updated_at >= ?", session.OpenedAt).
		Where("tenant_sales.status = ?", "cancelled")
	if session.ClosedAt != nil {
		q = q.Where("tenant_sales.updated_at <= ?", *session.ClosedAt)
	}
	if f.UserID > 0 {
		q = q.Where("tenant_sales.user_id = ?", f.UserID)
	}
	if f.DateFrom != nil {
		q = q.Where("tenant_sales.updated_at >= ?", f.DateFrom)
	}
	if f.DateTo != nil {
		q = q.Where("tenant_sales.updated_at <= ?", f.DateTo)
	}
	if f.PaymentMethod != "" {
		q = applyPaymentMethodFilter(q, "tenant_sale_payments.method", f.PaymentMethod)
	}
	var rows []salePayRow
	err := q.Order("tenant_sales.updated_at DESC").Scan(&rows).Error
	return rows, err
}
