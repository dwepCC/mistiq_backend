package service

import (
	"errors"
	"sort"
	"strings"
	"time"

	"tukifac/internal/sales/nvdisplay"
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/salescope"

	"gorm.io/gorm"
)

// SessionReport es el reporte de cierre/resumen de una sesión de caja.
type SessionReport struct {
	Session              SessionReportHeader   `json:"session"`
	IncomeDetail         []IncomeDetailRow      `json:"income_detail"`
	ExpenseDetail        []ExpenseDetailRow     `json:"expense_detail"`
	CancelledSalesDetail []CancelledSaleRow     `json:"cancelled_sales_detail"`
	TotalsByMethod       TotalsByMethodReport   `json:"totals_by_method"`
	Totals               SessionTotals          `json:"totals"`
	CashPhysical         SessionCashPhysical    `json:"cash_physical"`
	Electronic           SessionElectronic      `json:"electronic"`
	Detraction           SessionDetraction      `json:"detraction"`
}

// SessionDetraction ventas con detracción BN (SPOT): informativo, sin impacto en arqueo.
type SessionDetraction struct {
	TotalSPOT float64           `json:"total_spot"`
	Sales     []IncomeDetailRow `json:"sales"`
}

// SessionCashPhysical resumen y detalle de caja física (solo efectivo).
type SessionCashPhysical struct {
	OpeningBalance  float64            `json:"opening_balance"`
	TotalIncome     float64            `json:"total_income"`
	TotalExpense    float64            `json:"total_expense"`
	PhysicalBalance float64            `json:"physical_balance"`
	SalesTotal      float64            `json:"sales_total"`
	CashSales       []IncomeDetailRow  `json:"cash_sales"`
	ManualIncome    []IncomeDetailRow  `json:"manual_income"`
	Expenses        []ExpenseDetailRow `json:"expenses"`
}

// SessionElectronic ventas por medios no efectivos (Yape, Plin, tarjeta, etc.).
type SessionElectronic struct {
	TotalSales    float64           `json:"total_sales"`
	SalesByMethod []MethodTotal     `json:"sales_by_method"`
	Sales         []IncomeDetailRow `json:"sales"`
}

type SessionReportHeader struct {
	ID               uint       `json:"id"`
	BranchID         uint       `json:"branch_id"`
	BranchName       string     `json:"branch_name"`
	OpenedByUserID   uint       `json:"opened_by_user_id"`
	OpenedByUserName string     `json:"opened_by_user_name"`
	OpenedAt         time.Time  `json:"opened_at"`
	ClosedAt         *time.Time `json:"closed_at"`
	OpeningBalance   float64    `json:"opening_balance"`
	ClosingBalance   *float64   `json:"closing_balance"`
	Status           string     `json:"status"`
	Notes            string     `json:"notes"` // apertura; si hubo notas de cierre, se concatenan al cerrar la sesión
}

type IncomeDetailRow struct {
	Date           time.Time `json:"date"`
	Type           string    `json:"type"`
	DocNumber      string    `json:"doc_number"`
	Reference      string    `json:"reference"`
	Amount         float64   `json:"amount"`
	PaymentMethod  string    `json:"payment_method"`
}

type ExpenseDetailRow struct {
	Date           time.Time `json:"date"`
	Type           string    `json:"type"`
	DocNumber      string    `json:"doc_number"`
	Reference      string    `json:"reference"`
	Amount         float64   `json:"amount"`
	PaymentMethod  string    `json:"payment_method"`
}

// CancelledSaleRow venta anulada vinculada a la sesión (reversión en caja).
type CancelledSaleRow struct {
	Date          time.Time `json:"date"`
	DocNumber     string    `json:"doc_number"`
	Amount        float64   `json:"amount"`
	PaymentMethod string    `json:"payment_method"`
	Reason        string    `json:"reason"`
}

type TotalsByMethodReport struct {
	Sales     []MethodTotal `json:"sales"`
	Purchases []MethodTotal `json:"purchases"`
	Movements []MethodTotal `json:"movements"`
}

type MethodTotal struct {
	Method string  `json:"method"`
	Total  float64 `json:"total"`
}

type SessionTotals struct {
	TotalIncome          float64 `json:"total_income"`
	TotalExpense         float64 `json:"total_expense"`
	TotalSales           float64 `json:"total_sales"` // cobrado directo (sin SPOT)
	TotalSalesDirect     float64 `json:"total_sales_direct"`
	TotalDetractionSpot  float64 `json:"total_detraccion_spot"`
	TotalSalesCommercial float64 `json:"total_sales_commercial"` // directo + SPOT
	TotalPurchases       float64 `json:"total_purchases"`
	FinalBalance         float64 `json:"final_balance"`
}

// MovementReportRow es una fila del reporte de movimientos.
type MovementReportRow struct {
	Date          time.Time `json:"date"`
	Type          string    `json:"type"`
	DocNumber     string    `json:"doc_number"`
	ContactName   string    `json:"contact_name"`
	UserName      string    `json:"user_name"`
	BranchName    string    `json:"branch_name"`
	PaymentMethod string    `json:"payment_method"`
	Amount        float64   `json:"amount"`
	MovementID    uint      `json:"movement_id"`
	CashSessionID uint      `json:"cash_session_id"`
	Category      string    `json:"category"`
	CashReference string    `json:"cash_reference"` // referencia del registro en caja (antes de derivar documento)
	NotesDetail   string    `json:"notes_detail"`    // notas del movimiento en caja
}

// MovementChannelSummary totales de un canal (efectivo o electrónico).
type MovementChannelSummary struct {
	TotalRows       int64         `json:"total_rows"`
	SumIncome       float64       `json:"sum_income"`
	SumExpense      float64       `json:"sum_expense"`
	NetMovement     float64       `json:"net_movement"`
	OpeningBalance  *float64      `json:"opening_balance,omitempty"`
	PhysicalBalance *float64      `json:"physical_balance,omitempty"`
	SalesByMethod   []MethodTotal `json:"sales_by_method,omitempty"`
}

// MovementChannelBlock filas paginadas y resumen de un canal.
type MovementChannelBlock struct {
	Data    []MovementReportRow    `json:"data"`
	Total   int64                  `json:"total"`
	Summary MovementChannelSummary `json:"summary"`
}

// MovementsReportSplit respuesta separada: caja física, electrónico y detracción SPOT.
type MovementsReportSplit struct {
	Cash       MovementChannelBlock `json:"cash"`
	Electronic MovementChannelBlock `json:"electronic"`
	Detraction MovementChannelBlock `json:"detraction"`
}

// MovementReportFilters filtros para el reporte de movimientos.
type MovementReportFilters struct {
	BranchID      uint
	UserID        uint
	DateFrom      *time.Time
	DateTo        *time.Time
	SessionID     uint
	MovementType  string
	PaymentMethod string
	Limit         int
	Offset        int
}

func (s *CashBankService) movementReportFilteredDB(f MovementReportFilters) *gorm.DB {
	q := s.db.Model(&database.TenantCashMovement{}).
		Joins("LEFT JOIN tenant_cash_sessions ON tenant_cash_sessions.id = tenant_cash_movements.cash_session_id").
		Where("tenant_cash_movements.id > 0")

	if f.SessionID > 0 {
		q = q.Where("tenant_cash_movements.cash_session_id = ?", f.SessionID)
	}
	if f.BranchID > 0 {
		q = q.Where("tenant_cash_sessions.branch_id = ?", f.BranchID)
	}
	if f.UserID > 0 {
		q = q.Where("tenant_cash_movements.user_id = ?", f.UserID)
	}
	if f.MovementType != "" {
		q = q.Where("tenant_cash_movements.type = ?", f.MovementType)
	}
	if f.PaymentMethod != "" {
		q = applyPaymentMethodFilter(q, "tenant_cash_movements.payment_method", f.PaymentMethod)
	}
	if f.DateFrom != nil {
		q = q.Where("tenant_cash_movements.created_at >= ?", f.DateFrom)
	}
	if f.DateTo != nil {
		q = q.Where("tenant_cash_movements.created_at <= ?", f.DateTo)
	}
	return q
}

// GetSessionReport genera el reporte de cierre para una sesión de caja.
func (s *CashBankService) GetSessionReport(sessionID uint) (*SessionReport, error) {
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("sesión de caja no encontrada")
		}
		return nil, err
	}

	report := &SessionReport{
		Session: SessionReportHeader{
			ID:             session.ID,
			BranchID:       session.BranchID,
			OpenedByUserID: session.OpenedBy,
			OpenedAt:       session.OpenedAt,
			ClosedAt:       session.ClosedAt,
			OpeningBalance: session.OpeningBalance,
			ClosingBalance: session.ClosingBalance,
			Status:         session.Status,
			Notes:          session.Notes,
		},
	}

	var branch database.TenantBranch
	if s.db.First(&branch, session.BranchID).Error == nil {
		report.Session.BranchName = branch.Name
	}
	var user database.TenantUser
	if s.db.First(&user, session.OpenedBy).Error == nil {
		report.Session.OpenedByUserName = user.Name
	}

	var movements []database.TenantCashMovement
	if err := s.db.Where("cash_session_id = ?", sessionID).Order("created_at ASC").Find(&movements).Error; err != nil {
		return nil, err
	}

	salesByMethod := make(map[string]float64)
	purchasesByMethod := make(map[string]float64)
	manualIncomeByMethod := make(map[string]float64)
	manualExpenseByMethod := make(map[string]float64)

	var sessionSales []database.TenantSale
	if err := s.db.Where("cash_session_id = ? AND status NOT IN ?", sessionID, []string{"cancelled", "draft"}).
		Order("created_at ASC").Find(&sessionSales).Error; err != nil {
		return nil, err
	}
	orphanSales, _ := s.listOrphanSalesForSession(&session)
	seenSale := make(map[uint]struct{}, len(sessionSales))
	for _, sale := range sessionSales {
		seenSale[sale.ID] = struct{}{}
	}
	for _, sale := range orphanSales {
		if _, ok := seenSale[sale.ID]; ok {
			continue
		}
		sessionSales = append(sessionSales, sale)
	}
	salesMap := make(map[uint]database.TenantSale, len(sessionSales))
	saleIDs := make([]uint, 0, len(sessionSales))
	for _, sale := range sessionSales {
		salesMap[sale.ID] = sale
		saleIDs = append(saleIDs, sale.ID)
	}
	displayDocNumbers := nvdisplay.LoadDisplayNumbersBySaleID(s.db, saleIDs)
	if len(saleIDs) > 0 {
		var payments []database.TenantSalePayment
		s.db.Where("sale_id IN ?", saleIDs).Order("created_at ASC").Find(&payments)
		reportAmounts := buildSalePaymentReportAmountsFromPayments(salesMap, payments)
		paidBySale := make(map[uint]float64)
		for _, p := range payments {
			meth := normalizeReportMethod(p.Method)
			sale := salesMap[p.SaleID]
			reportAmt := paymentReportAmount(p.Amount, p.ID, reportAmounts)
			if IsDetractionPaymentMethod(meth) || IsDetractionPaymentMethod(p.Method) {
				report.Totals.TotalDetractionSpot += p.Amount
				report.Totals.TotalSalesCommercial += p.Amount
				report.Detraction.TotalSPOT += p.Amount
				report.Detraction.Sales = append(report.Detraction.Sales, IncomeDetailRow{
					Date:          p.CreatedAt,
					Type:          "detraccion_spot",
					DocNumber:     displayDocNumbers[sale.ID],
					Reference:     p.Reference,
					Amount:        p.Amount,
					PaymentMethod: meth,
				})
				continue
			}
			salesByMethod[meth] += reportAmt
			report.Totals.TotalSales += reportAmt
			report.Totals.TotalSalesDirect += reportAmt
			report.Totals.TotalSalesCommercial += reportAmt
			paidBySale[p.SaleID] += reportAmt
			report.IncomeDetail = append(report.IncomeDetail, IncomeDetailRow{
				Date:          p.CreatedAt,
				Type:          "venta",
				DocNumber:     displayDocNumbers[sale.ID],
				Reference:     p.Reference,
				Amount:        reportAmt,
				PaymentMethod: meth,
			})
		}
		for _, sale := range sessionSales {
			if paidBySale[sale.ID] == 0 && sale.Total != 0 {
				meth := normalizeReportMethod(sale.PaymentMethod)
				if IsDetractionPaymentMethod(meth) {
					continue
				}
				salesByMethod[meth] += sale.Total
				report.Totals.TotalSales += sale.Total
				report.Totals.TotalSalesDirect += sale.Total
				report.Totals.TotalSalesCommercial += sale.Total
				report.IncomeDetail = append(report.IncomeDetail, IncomeDetailRow{
					Date:          sale.CreatedAt,
					Type:          "venta",
					DocNumber:     displayDocNumbers[sale.ID],
					Amount:        sale.Total,
					PaymentMethod: meth,
				})
			}
		}
	}

	for _, m := range movements {
		paymentMethod := normalizeReportMethod(m.PaymentMethod)

		if m.Type == "income" {
			report.Totals.TotalIncome += m.Amount
			if m.SaleID != nil {
				continue
			}
			row := IncomeDetailRow{Date: m.CreatedAt, Amount: m.Amount, PaymentMethod: paymentMethod}
			if m.Category == "ingreso_manual" || m.Category == "Ingreso manual" {
				row.Type = "ingreso_manual"
			} else {
				row.Type = "otro"
			}
			row.Reference = m.Reference
			manualIncomeByMethod[paymentMethod] += m.Amount
			report.IncomeDetail = append(report.IncomeDetail, row)
		} else {
			report.Totals.TotalExpense += m.Amount
			row := ExpenseDetailRow{Date: m.CreatedAt, Amount: m.Amount, PaymentMethod: paymentMethod}
			if m.PurchaseID != nil {
				row.Type = "compra"
				report.Totals.TotalPurchases += m.Amount
				purchasesByMethod[paymentMethod] += m.Amount
				var pur database.TenantPurchase
				if s.db.First(&pur, *m.PurchaseID).Error == nil {
					row.DocNumber = pur.Series + "-" + pur.Number
				}
				report.ExpenseDetail = append(report.ExpenseDetail, row)
			} else {
				if m.Category == "gasto" || m.Category == "Gasto" {
					row.Type = "gasto"
				} else {
					row.Type = "egreso_manual"
				}
				row.Reference = m.Reference
				manualExpenseByMethod[paymentMethod] += m.Amount
				report.ExpenseDetail = append(report.ExpenseDetail, row)
			}
		}
	}

	report.Totals.FinalBalance = report.Session.OpeningBalance + report.Totals.TotalIncome - report.Totals.TotalExpense
	report.TotalsByMethod.Sales = mapToMethodTotals(salesByMethod)
	report.TotalsByMethod.Purchases = mapToMethodTotals(purchasesByMethod)
	for k, v := range manualIncomeByMethod {
		report.TotalsByMethod.Movements = append(report.TotalsByMethod.Movements, MethodTotal{Method: k, Total: v})
	}
	for k, v := range manualExpenseByMethod {
		report.TotalsByMethod.Movements = append(report.TotalsByMethod.Movements, MethodTotal{Method: k, Total: -v})
	}

	var voidMovements []database.TenantCashMovement
	s.db.Where("cash_session_id = ? AND type = ? AND category = ?", sessionID, "expense", "Anulación venta").
		Order("created_at ASC").
		Find(&voidMovements)
	for _, m := range voidMovements {
		row := CancelledSaleRow{
			Date:          m.CreatedAt,
			Amount:        m.Amount,
			PaymentMethod: normalizeReportMethod(m.PaymentMethod),
			Reason:        strings.TrimSpace(m.Notes),
		}
		if m.SaleID != nil {
			var sale database.TenantSale
			if s.db.First(&sale, *m.SaleID).Error == nil {
				row.DocNumber = sale.Number
				if row.Reason == "" && strings.Contains(sale.Notes, "ANULADA:") {
					row.Reason = sale.Notes
				}
			}
		}
		if row.DocNumber == "" {
			row.DocNumber = m.Reference
		}
		report.CancelledSalesDetail = append(report.CancelledSalesDetail, row)
	}

	if err := s.appendCancelledSalesFromPayments(report, &session, voidMovements); err != nil {
		return nil, err
	}

	populateSessionReportSections(report)
	return report, nil
}

func populateSessionReportSections(r *SessionReport) {
	cashSales := make([]IncomeDetailRow, 0)
	electronicSales := make([]IncomeDetailRow, 0)
	manualIncome := make([]IncomeDetailRow, 0)
	electronicByMethod := make(map[string]float64)
	var cashSalesTotal, electronicTotal float64

	for _, row := range r.IncomeDetail {
		switch row.Type {
		case "venta":
			if IsDetractionPaymentMethod(row.PaymentMethod) {
				continue
			}
			if IsCashPaymentMethod(row.PaymentMethod) {
				cashSales = append(cashSales, row)
				cashSalesTotal += row.Amount
			} else {
				electronicSales = append(electronicSales, row)
				electronicTotal += row.Amount
				electronicByMethod[row.PaymentMethod] += row.Amount
			}
		default:
			manualIncome = append(manualIncome, row)
		}
	}

	r.CashPhysical = SessionCashPhysical{
		OpeningBalance:  r.Session.OpeningBalance,
		TotalIncome:     r.Totals.TotalIncome,
		TotalExpense:    r.Totals.TotalExpense,
		PhysicalBalance: r.Totals.FinalBalance,
		SalesTotal:      cashSalesTotal,
		CashSales:       cashSales,
		ManualIncome:    manualIncome,
		Expenses:        append([]ExpenseDetailRow{}, r.ExpenseDetail...),
	}
	r.Electronic = SessionElectronic{
		TotalSales:    electronicTotal,
		SalesByMethod: mapToMethodTotals(electronicByMethod),
		Sales:         electronicSales,
	}
}

func mapToMethodTotals(m map[string]float64) []MethodTotal {
	out := make([]MethodTotal, 0, len(m))
	for k, v := range m {
		if v != 0 {
			out = append(out, MethodTotal{Method: k, Total: v})
		}
	}
	return out
}

// ListMovementsReport devuelve movimientos separados: caja física (efectivo) y medios electrónicos.
func (s *CashBankService) ListMovementsReport(f MovementReportFilters) (MovementsReportSplit, error) {
	saleRows, err := s.buildSalePaymentMovementRows(f)
	if err != nil {
		return MovementsReportSplit{}, err
	}
	cashRows, err := s.buildCashMovementReportRows(f)
	if err != nil {
		return MovementsReportSplit{}, err
	}
	cancelledElectronicRows, err := s.buildCancelledElectronicMovementRows(f)
	if err != nil {
		return MovementsReportSplit{}, err
	}

	all := append(saleRows, cashRows...)
	all = append(all, cancelledElectronicRows...)
	sortMovementRowsDesc(all)

	var cashChannel, electronicChannel, detractionChannel []MovementReportRow
	for _, row := range all {
		ch := movementRowChannel(row)
		switch ch {
		case "detraction":
			detractionChannel = append(detractionChannel, row)
		case "electronic":
			electronicChannel = append(electronicChannel, row)
		default:
			cashChannel = append(cashChannel, row)
		}
	}

	var opening *float64
	if f.SessionID > 0 {
		var sess database.TenantCashSession
		if s.db.First(&sess, f.SessionID).Error == nil {
			ob := sess.OpeningBalance
			opening = &ob
		}
	}

	// Los canales cash/electronic siempre devuelven todas las filas; la paginación
	// compartida vaciaba el bloque electrónico cuando había muchas filas de efectivo.
	return MovementsReportSplit{
		Cash:       buildMovementChannelBlock(cashChannel, 0, 0, opening),
		Electronic: buildMovementChannelBlock(electronicChannel, 0, 0, nil),
		Detraction: buildMovementChannelBlock(detractionChannel, 0, 0, nil),
	}, nil
}

func buildMovementChannelBlock(rows []MovementReportRow, limit, offset int, opening *float64) MovementChannelBlock {
	salesByMethod := make(map[string]float64)
	var sumIn, sumEx float64
	for _, r := range rows {
		if r.Amount >= 0 {
			sumIn += r.Amount
			if r.Type == "venta" {
				salesByMethod[r.PaymentMethod] += r.Amount
			}
		} else {
			sumEx += -r.Amount
		}
	}
	net := sumIn - sumEx
	summary := MovementChannelSummary{
		TotalRows:     int64(len(rows)),
		SumIncome:     sumIn,
		SumExpense:    sumEx,
		NetMovement:   net,
		SalesByMethod: mapToMethodTotals(salesByMethod),
	}
	if opening != nil {
		summary.OpeningBalance = opening
		pb := *opening + net
		summary.PhysicalBalance = &pb
	}

	data := rows
	if limit > 0 {
		start := offset
		if start > len(rows) {
			data = []MovementReportRow{}
		} else {
			end := start + limit
			if end > len(rows) {
				end = len(rows)
			}
			data = rows[start:end]
		}
	}

	return MovementChannelBlock{
		Data:    data,
		Total:   int64(len(rows)),
		Summary: summary,
	}
}

func sortMovementRowsDesc(rows []MovementReportRow) {
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Date.After(rows[j].Date)
	})
}

type salePayRow struct {
	PaymentID         uint
	SaleID            uint
	Method            string
	Amount            float64
	Reference         string
	Notes             string
	CreatedAt         time.Time
	SaleNumber        string
	SaleUserID        uint
	ContactID         *uint
	CashSessionID     uint
	SalePaymentMethod string
	SaleCreatedAt     time.Time
	SaleNotes         string
	BranchID          uint
}

// listOrphanSalesForSession ventas del turno sin cash_session_id (cualquier usuario de la sucursal).
// Incluye ventas del administrador u otros sin caja propia registradas durante el turno.
func (s *CashBankService) listOrphanSalesForSession(session *database.TenantCashSession) ([]database.TenantSale, error) {
	if session == nil || session.ID == 0 {
		return nil, nil
	}
	q := salescope.CommercialSales(s.db.Model(&database.TenantSale{})).
		Where("(cash_session_id IS NULL OR cash_session_id = 0)").
		Where("branch_id = ?", session.BranchID).
		Where("created_at >= ?", session.OpenedAt).
		Where("status NOT IN ?", []string{"cancelled", "draft"})
	if session.ClosedAt != nil {
		q = q.Where("created_at <= ?", *session.ClosedAt)
	}
	var sales []database.TenantSale
	err := q.Order("created_at ASC").Find(&sales).Error
	return sales, err
}

func (s *CashBankService) scanOrphanSalePaymentRows(f MovementReportFilters) ([]salePayRow, error) {
	var session database.TenantCashSession
	if err := s.db.First(&session, f.SessionID).Error; err != nil {
		return nil, nil
	}
	q := s.db.Table("tenant_sale_payments").
		Select(`tenant_sale_payments.id AS payment_id, tenant_sale_payments.sale_id, tenant_sale_payments.method,
			tenant_sale_payments.amount, tenant_sale_payments.reference, tenant_sale_payments.notes, tenant_sale_payments.created_at,
			tenant_sales.number AS sale_number, tenant_sales.user_id AS sale_user_id, tenant_sales.contact_id,
			? AS cash_session_id, tenant_sales.payment_method AS sale_payment_method, tenant_sales.created_at AS sale_created_at,
			? AS branch_id`, f.SessionID, session.BranchID).
		Joins("JOIN tenant_sales ON tenant_sales.id = tenant_sale_payments.sale_id").
		Where("(tenant_sales.cash_session_id IS NULL OR tenant_sales.cash_session_id = 0)").
		Scopes(salescope.ScopeCommercial("tenant_sales")).
		Where("tenant_sales.branch_id = ?", session.BranchID).
		Where("tenant_sales.created_at >= ?", session.OpenedAt).
		Where("tenant_sales.status NOT IN ?", []string{"cancelled", "draft"})
	if session.ClosedAt != nil {
		q = q.Where("tenant_sales.created_at <= ?", *session.ClosedAt)
	}
	if f.UserID > 0 {
		q = q.Where("tenant_sales.user_id = ?", f.UserID)
	}
	if f.PaymentMethod != "" {
		q = applyPaymentMethodFilter(q, "tenant_sale_payments.method", f.PaymentMethod)
	}
	var rows []salePayRow
	err := q.Order("tenant_sale_payments.created_at DESC").Scan(&rows).Error
	return rows, err
}

// scanUnlinkedSalePaymentRows ventas sin sesión de caja en rango de fechas (reporte de movimientos sin filtrar por sesión).
func (s *CashBankService) scanUnlinkedSalePaymentRows(f MovementReportFilters) ([]salePayRow, error) {
	if f.SessionID > 0 {
		return nil, nil
	}
	q := s.db.Table("tenant_sale_payments").
		Select(`tenant_sale_payments.id AS payment_id, tenant_sale_payments.sale_id, tenant_sale_payments.method,
			tenant_sale_payments.amount, tenant_sale_payments.reference, tenant_sale_payments.notes, tenant_sale_payments.created_at,
			tenant_sales.number AS sale_number, tenant_sales.user_id AS sale_user_id, tenant_sales.contact_id,
			0 AS cash_session_id, tenant_sales.payment_method AS sale_payment_method, tenant_sales.created_at AS sale_created_at,
			tenant_sales.branch_id`).
		Joins("JOIN tenant_sales ON tenant_sales.id = tenant_sale_payments.sale_id").
		Where("(tenant_sales.cash_session_id IS NULL OR tenant_sales.cash_session_id = 0)").
		Scopes(salescope.ScopeCommercial("tenant_sales")).
		Where("tenant_sales.status NOT IN ?", []string{"cancelled", "draft"})
	if f.BranchID > 0 {
		q = q.Where("tenant_sales.branch_id = ?", f.BranchID)
	}
	if f.UserID > 0 {
		q = q.Where("tenant_sales.user_id = ?", f.UserID)
	}
	if f.DateFrom != nil {
		q = q.Where("tenant_sales.created_at >= ?", f.DateFrom)
	}
	if f.DateTo != nil {
		q = q.Where("tenant_sales.created_at <= ?", f.DateTo)
	}
	if f.PaymentMethod != "" {
		q = applyPaymentMethodFilter(q, "tenant_sale_payments.method", f.PaymentMethod)
	}
	var rows []salePayRow
	err := q.Order("tenant_sale_payments.created_at DESC").Scan(&rows).Error
	return rows, err
}

func (s *CashBankService) buildSalePaymentMovementRows(f MovementReportFilters) ([]MovementReportRow, error) {
	if f.MovementType == "expense" {
		return nil, nil
	}
	q := s.db.Table("tenant_sale_payments").
		Select(`tenant_sale_payments.id AS payment_id, tenant_sale_payments.sale_id, tenant_sale_payments.method,
			tenant_sale_payments.amount, tenant_sale_payments.reference, tenant_sale_payments.notes, tenant_sale_payments.created_at,
			tenant_sales.number AS sale_number, tenant_sales.user_id AS sale_user_id, tenant_sales.contact_id,
			tenant_sales.cash_session_id, tenant_sales.payment_method AS sale_payment_method, tenant_sales.created_at AS sale_created_at,
			tenant_cash_sessions.branch_id`).
		Joins("JOIN tenant_sales ON tenant_sales.id = tenant_sale_payments.sale_id").
		Joins("JOIN tenant_cash_sessions ON tenant_cash_sessions.id = tenant_sales.cash_session_id").
		Where("tenant_sales.cash_session_id > 0").
		Where("tenant_sales.status NOT IN ?", []string{"cancelled", "draft"})

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
		q = q.Where("tenant_sales.created_at >= ?", f.DateFrom)
	}
	if f.DateTo != nil {
		q = q.Where("tenant_sales.created_at <= ?", f.DateTo)
	}
	if f.PaymentMethod != "" {
		q = applyPaymentMethodFilter(q, "tenant_sale_payments.method", f.PaymentMethod)
	}

	var payRows []salePayRow
	if err := q.Order("tenant_sale_payments.created_at DESC").Scan(&payRows).Error; err != nil {
		return nil, err
	}
	var extra []salePayRow
	var extraErr error
	if f.SessionID > 0 {
		extra, extraErr = s.scanOrphanSalePaymentRows(f)
	} else {
		extra, extraErr = s.scanUnlinkedSalePaymentRows(f)
	}
	if extraErr != nil {
		return nil, extraErr
	}
	if len(extra) > 0 {
		seen := make(map[uint]struct{}, len(payRows))
		for _, p := range payRows {
			seen[p.PaymentID] = struct{}{}
		}
		for _, p := range extra {
			if _, ok := seen[p.PaymentID]; ok {
				continue
			}
			payRows = append(payRows, p)
		}
	}

	userIDs := make(map[uint]struct{})
	contactIDs := make(map[uint]struct{})
	branchIDs := make(map[uint]struct{})
	for _, p := range payRows {
		userIDs[p.SaleUserID] = struct{}{}
		if p.ContactID != nil {
			contactIDs[*p.ContactID] = struct{}{}
		}
		branchIDs[p.BranchID] = struct{}{}
	}
	users := loadUserNamesMap(s.db, userIDs)
	contacts := loadContactNamesMap(s.db, contactIDs)
	branches := loadBranchNamesMap(s.db, branchIDs)

	saleIDsForDisplay := make([]uint, 0, len(payRows))
	for _, p := range payRows {
		saleIDsForDisplay = append(saleIDsForDisplay, p.SaleID)
	}
	reportAmounts, _ := s.buildSalePaymentReportAmountMap(saleIDsForDisplay)

	rows := make([]MovementReportRow, 0, len(payRows))

	// Ventas legacy sin líneas en tenant_sale_payments
	legacyQ := s.db.Model(&database.TenantSale{}).
		Joins("JOIN tenant_cash_sessions ON tenant_cash_sessions.id = tenant_sales.cash_session_id").
		Where("tenant_sales.cash_session_id > 0").
		Where("tenant_sales.status NOT IN ?", []string{"cancelled", "draft"}).
		Where("NOT EXISTS (SELECT 1 FROM tenant_sale_payments sp WHERE sp.sale_id = tenant_sales.id)")
	if f.SessionID > 0 {
		legacyQ = legacyQ.Where("tenant_sales.cash_session_id = ?", f.SessionID)
	}
	if f.BranchID > 0 {
		legacyQ = legacyQ.Where("tenant_cash_sessions.branch_id = ?", f.BranchID)
	}
	if f.UserID > 0 {
		legacyQ = legacyQ.Where("tenant_sales.user_id = ?", f.UserID)
	}
	if f.DateFrom != nil {
		legacyQ = legacyQ.Where("tenant_sales.created_at >= ?", f.DateFrom)
	}
	if f.DateTo != nil {
		legacyQ = legacyQ.Where("tenant_sales.created_at <= ?", f.DateTo)
	}
	if f.PaymentMethod != "" {
		legacyQ = applyPaymentMethodFilter(legacyQ, "tenant_sales.payment_method", f.PaymentMethod)
	}
	var legacySales []database.TenantSale
	if err := legacyQ.Order("tenant_sales.created_at DESC").Find(&legacySales).Error; err != nil {
		return nil, err
	}
	for _, sale := range legacySales {
		saleIDsForDisplay = append(saleIDsForDisplay, sale.ID)
	}
	displayDocNumbers := nvdisplay.LoadDisplayNumbersBySaleID(s.db, saleIDsForDisplay)

	for _, p := range payRows {
		meth := normalizeReportMethod(p.Method)
		contactName := ""
		if p.ContactID != nil {
			contactName = contacts[*p.ContactID]
		}
		docNum := displayDocNumbers[p.SaleID]
		if docNum == "" {
			docNum = p.SaleNumber
		}
		rows = append(rows, MovementReportRow{
			Date:          p.CreatedAt,
			Type:          "venta",
			DocNumber:     docNum,
			ContactName:   contactName,
			UserName:      users[p.SaleUserID],
			BranchName:    branches[p.BranchID],
			PaymentMethod: meth,
			Amount:        paymentReportAmount(p.Amount, p.PaymentID, reportAmounts),
			MovementID:    salePaymentMovementID(p.PaymentID),
			CashSessionID: p.CashSessionID,
			Category:      "Venta",
			CashReference: p.Reference,
			NotesDetail:   p.Notes,
		})
	}

	// Ventas legacy sin líneas en tenant_sale_payments
	for _, sale := range legacySales {
		if sale.CashSessionID == nil {
			continue
		}
		meth := normalizeReportMethod(sale.PaymentMethod)
		contactName := ""
		if sale.ContactID != nil {
			var c database.TenantContact
			if s.db.First(&c, *sale.ContactID).Error == nil {
				contactName = contactDisplayName(c)
			}
		}
		branchName := ""
		var ses database.TenantCashSession
		if s.db.First(&ses, *sale.CashSessionID).Error == nil {
			var b database.TenantBranch
			if s.db.First(&b, ses.BranchID).Error == nil {
				branchName = b.Name
			}
		}
		userName := ""
		var u database.TenantUser
		if s.db.First(&u, sale.UserID).Error == nil {
			userName = u.Name
		}
		rows = append(rows, MovementReportRow{
			Date:          sale.CreatedAt,
			Type:          "venta",
			DocNumber:     displayDocNumbers[sale.ID],
			ContactName:   contactName,
			UserName:      userName,
			BranchName:    branchName,
			PaymentMethod: meth,
			Amount:        sale.Total,
			MovementID:    salePaymentMovementID(sale.ID),
			CashSessionID: *sale.CashSessionID,
			Category:      "Venta",
		})
	}
	return rows, nil
}

func (s *CashBankService) buildCashMovementReportRows(f MovementReportFilters) ([]MovementReportRow, error) {
	base := s.movementReportFilteredDB(f)
	// Las ventas se listan desde tenant_sale_payments; en caja física solo manuales, compras y anulaciones.
	base = base.Where(
		"(tenant_cash_movements.sale_id IS NULL OR tenant_cash_movements.sale_id = 0 OR tenant_cash_movements.type = ?)",
		"expense",
	)
	var movements []database.TenantCashMovement
	if err := base.Order("tenant_cash_movements.created_at DESC").Find(&movements).Error; err != nil {
		return nil, err
	}

	sessionIDs := make(map[uint]struct{})
	userIDs := make(map[uint]struct{})
	purchaseIDs := make(map[uint]struct{})
	for _, m := range movements {
		sessionIDs[m.CashSessionID] = struct{}{}
		userIDs[m.UserID] = struct{}{}
		if m.PurchaseID != nil {
			purchaseIDs[*m.PurchaseID] = struct{}{}
		}
	}

	sessions := make(map[uint]database.TenantCashSession)
	if len(sessionIDs) > 0 {
		var list []database.TenantCashSession
		s.db.Where("id IN ?", keysUint(sessionIDs)).Find(&list)
		for _, se := range list {
			sessions[se.ID] = se
		}
	}
	branchIDs := make(map[uint]struct{})
	for _, ses := range sessions {
		branchIDs[ses.BranchID] = struct{}{}
	}
	branches := loadBranchNamesMap(s.db, branchIDs)
	users := loadUserNamesMap(s.db, userIDs)

	purchases := make(map[uint]database.TenantPurchase)
	if len(purchaseIDs) > 0 {
		var list []database.TenantPurchase
		s.db.Where("id IN ?", keysUint(purchaseIDs)).Find(&list)
		for _, p := range list {
			purchases[p.ID] = p
		}
	}
	contactsByPurchase := make(map[uint]string)
	for pid, pur := range purchases {
		if pur.ContactID != nil {
			var c database.TenantContact
			if s.db.First(&c, *pur.ContactID).Error == nil {
				contactsByPurchase[pid] = contactDisplayName(c)
			}
		}
	}

	var rows []MovementReportRow
	for _, m := range movements {
		row := MovementReportRow{
			Date:          m.CreatedAt,
			Amount:        m.Amount,
			MovementID:    m.ID,
			CashSessionID: m.CashSessionID,
			Category:      m.Category,
			CashReference: m.Reference,
			NotesDetail:   m.Notes,
		}
		if m.Type == "expense" {
			row.Amount = -m.Amount
		}
		if ses, ok := sessions[m.CashSessionID]; ok {
			row.BranchName = branches[ses.BranchID]
		}
		row.UserName = users[m.UserID]
		row.PaymentMethod = normalizeReportMethod(m.PaymentMethod)

		if m.SaleID != nil && m.Type == "expense" {
			row.Type = "anulacion_venta"
			var sale database.TenantSale
			if s.db.First(&sale, *m.SaleID).Error == nil {
				row.DocNumber = sale.Number
			}
		} else if m.PurchaseID != nil {
			row.Type = "compra"
			if pur, ok := purchases[*m.PurchaseID]; ok {
				row.DocNumber = pur.Series + "-" + pur.Number
				row.ContactName = contactsByPurchase[*m.PurchaseID]
			}
		} else if m.Type == "income" {
			row.Type = "ingreso"
			row.DocNumber = m.Reference
			row.ContactName = m.Notes
		} else {
			row.Type = "egreso"
			row.DocNumber = m.Reference
			row.ContactName = m.Notes
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func loadUserNamesMap(db *gorm.DB, ids map[uint]struct{}) map[uint]string {
	out := make(map[uint]string)
	if len(ids) == 0 {
		return out
	}
	var list []database.TenantUser
	db.Where("id IN ?", keysUint(ids)).Find(&list)
	for _, u := range list {
		out[u.ID] = u.Name
	}
	return out
}

func loadBranchNamesMap(db *gorm.DB, ids map[uint]struct{}) map[uint]string {
	out := make(map[uint]string)
	if len(ids) == 0 {
		return out
	}
	var list []database.TenantBranch
	db.Where("id IN ?", keysUint(ids)).Find(&list)
	for _, b := range list {
		out[b.ID] = b.Name
	}
	return out
}

func loadContactNamesMap(db *gorm.DB, ids map[uint]struct{}) map[uint]string {
	out := make(map[uint]string)
	if len(ids) == 0 {
		return out
	}
	var list []database.TenantContact
	db.Where("id IN ?", keysUint(ids)).Find(&list)
	for _, c := range list {
		out[c.ID] = contactDisplayName(c)
	}
	return out
}

// SessionProductSoldRow producto vendido agregado por sesión de caja.
type SessionProductSoldRow struct {
	ProductID   *uint   `json:"product_id"`
	Code        string  `json:"code"`
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	Total       float64 `json:"total"`
}

// GetSessionProductsReport agrega ítems vendidos vinculados a una sesión de caja.
func (s *CashBankService) GetSessionProductsReport(sessionID uint) ([]SessionProductSoldRow, error) {
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("sesión de caja no encontrada")
		}
		return nil, err
	}
	var rows []SessionProductSoldRow
	err := s.db.Table("tenant_sale_items").
		Select(`tenant_sale_items.product_id, tenant_sale_items.code, tenant_sale_items.description,
			SUM(tenant_sale_items.quantity) AS quantity, SUM(tenant_sale_items.total) AS total`).
		Joins("JOIN tenant_sales ON tenant_sales.id = tenant_sale_items.sale_id").
		Scopes(salescope.ScopeCommercial("tenant_sales")).
		Where("tenant_sales.cash_session_id = ? AND tenant_sales.status NOT IN ?", sessionID, []string{"cancelled", "draft"}).
		Group("tenant_sale_items.product_id, tenant_sale_items.code, tenant_sale_items.description").
		Order("tenant_sale_items.description ASC").
		Scan(&rows).Error
	return rows, err
}

func buildSalePaymentReportAmountsFromPayments(
	salesMap map[uint]database.TenantSale,
	payments []database.TenantSalePayment,
) map[uint]float64 {
	bySale := make(map[uint][]database.TenantSalePayment)
	for _, p := range payments {
		bySale[p.SaleID] = append(bySale[p.SaleID], p)
	}
	out := make(map[uint]float64)
	for saleID, pList := range bySale {
		sale, ok := salesMap[saleID]
		if !ok {
			for _, p := range pList {
				out[p.ID] = p.Amount
			}
			continue
		}
		lines := make([]money.SalePaymentLine, len(pList))
		for i, p := range pList {
			lines[i] = money.SalePaymentLine{ID: p.ID, Amount: p.Amount}
		}
		for id, amt := range money.AllocateSalePaymentReportAmounts(sale.Total, lines) {
			out[id] = amt
		}
	}
	return out
}

func (s *CashBankService) buildSalePaymentReportAmountMap(saleIDs []uint) (map[uint]float64, error) {
	out := make(map[uint]float64)
	if len(saleIDs) == 0 {
		return out, nil
	}
	unique := make(map[uint]struct{}, len(saleIDs))
	for _, id := range saleIDs {
		if id > 0 {
			unique[id] = struct{}{}
		}
	}
	ids := keysUint(unique)
	if len(ids) == 0 {
		return out, nil
	}
	var sales []database.TenantSale
	if err := s.db.Where("id IN ?", ids).Find(&sales).Error; err != nil {
		return nil, err
	}
	salesMap := make(map[uint]database.TenantSale, len(sales))
	for _, sale := range sales {
		salesMap[sale.ID] = sale
	}
	var payments []database.TenantSalePayment
	if err := s.db.Where("sale_id IN ?", ids).Order("created_at ASC").Find(&payments).Error; err != nil {
		return nil, err
	}
	return buildSalePaymentReportAmountsFromPayments(salesMap, payments), nil
}

func paymentReportAmount(raw float64, paymentID uint, reportAmounts map[uint]float64) float64 {
	if reportAmounts == nil {
		return raw
	}
	if amt, ok := reportAmounts[paymentID]; ok {
		return amt
	}
	return raw
}

func keysUint(m map[uint]struct{}) []uint {
	keys := make([]uint, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func contactDisplayName(c database.TenantContact) string {
	if c.TradeName != "" {
		return c.TradeName
	}
	return c.BusinessName
}
