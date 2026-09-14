package service

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"tukifac/internal/sales/nvdisplay"
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/salescope"

	"gorm.io/gorm"
)

// SessionReport es el reporte de cierre/resumen de una sesión de caja.
type SessionReport struct {
	Session              SessionReportHeader     `json:"session"`
	IncomeDetail         []IncomeDetailRow       `json:"income_detail"`
	ExpenseDetail        []ExpenseDetailRow      `json:"expense_detail"`
	CancelledSalesDetail []CancelledSaleRow      `json:"cancelled_sales_detail"`
	TotalsByMethod       TotalsByMethodReport    `json:"totals_by_method"`
	Totals               SessionTotals           `json:"totals"`
	CashPhysical         SessionCashPhysical     `json:"cash_physical"`
	Electronic           SessionElectronic       `json:"electronic"`
	Detraction           SessionDetraction       `json:"detraction"`
	CreditGenerated      SessionCreditGenerated  `json:"credit_generated"`
	PayableGenerated     SessionPayableGenerated `json:"payable_generated"`
}

// SessionPayableGenerated compras a crédito (CxP — Fase 2) REGISTRADAS en esta sesión, con su
// monto original — no representan dinero pagado, se excluyen de ExpenseDetail/TotalPurchases/
// TotalExpense (mismo criterio que CreditGenerated del lado de ventas). El saldo pendiente real
// (cuánto de esto ya se pagó, en qué sesión) se consulta en internal/payables (CxP). Informativo,
// sin impacto en arqueo.
type SessionPayableGenerated struct {
	Total     float64            `json:"total"`
	Purchases []ExpenseDetailRow `json:"purchases"`
}

// SessionCreditGenerated ventas registradas a crédito (condición "credito"/"credit") sin cobrar
// en esta sesión — no representan dinero recibido. Se excluyen explícitamente de
// IncomeDetail/TotalSales/TotalSalesDirect/TotalSalesCommercial/Electronic (mismo criterio que ya
// se aplica a la detracción SPOT) para no mostrarlas como ingreso electrónico ni de ningún otro
// tipo: el saldo pendiente real se consulta en internal/receivables (CxC). Informativo, sin
// impacto en arqueo.
type SessionCreditGenerated struct {
	Total float64           `json:"total"`
	Sales []IncomeDetailRow `json:"sales"`
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
	Date          time.Time `json:"date"`
	Type          string    `json:"type"`
	DocNumber     string    `json:"doc_number"`
	Reference     string    `json:"reference"`
	Amount        float64   `json:"amount"`
	PaymentMethod string    `json:"payment_method"`
	// SaleCashSessionID: Caja donde se REGISTRÓ la venta — presente únicamente en filas
	// Type="cobro_cxc" (un cobro que ocurrió en esta sesión de una venta registrada en OTRA),
	// para trazabilidad hacia el documento original. nil en "venta" (contado, misma Caja).
	SaleCashSessionID *uint `json:"sale_cash_session_id,omitempty"`
}

type ExpenseDetailRow struct {
	Date          time.Time `json:"date"`
	Type          string    `json:"type"`
	DocNumber     string    `json:"doc_number"`
	Reference     string    `json:"reference"`
	Amount        float64   `json:"amount"`
	PaymentMethod string    `json:"payment_method"`
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
	NotesDetail   string    `json:"notes_detail"`   // notas del movimiento en caja
	// SaleID/PurchaseID: documento de origen cuando el movimiento viene de una venta o compra —
	// el frontend los usa para imprimir el comprobante REAL de ese documento (ticket fiscal para
	// ventas) en vez del recibo interno genérico de caja. nil cuando el movimiento es manual.
	SaleID     *uint `json:"sale_id,omitempty"`
	PurchaseID *uint `json:"purchase_id,omitempty"`
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
	seenPurchaseIDs := make(map[uint]struct{})

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
	// (1) SOLO para "crédito generado" y detracción SPOT: TODOS los pagos de las ventas
	// REGISTRADAS en esta sesión, sin importar en qué Caja ocurrió cada pago. Esto es
	// deliberado — el saldo de crédito pendiente de una venta es "total - pagado en CUALQUIER
	// Caja", no "pagado en esta Caja" — pero, a partir de esta corrección, este lazo YA NO
	// alimenta IncomeDetail/TotalSales/TotalsByMethod.Sales/Electronic: eso mezclaba "actividad
	// comercial del documento" (dónde se registró la venta) con "dinero recibido" (dónde ocurrió
	// cada pago) — confirmado con datos reales de MySQL: un cobro posterior en otra Caja se
	// contaba como ingreso de la Caja de REGISTRO, y un cobro electrónico en la Caja que sí lo
	// recibió no aparecía en ningún lado de su propio reporte. Ese dinero ahora se calcula en el
	// bloque (2), más abajo, filtrando por TenantSalePayment.CashSessionID.
	if len(saleIDs) > 0 {
		var payments []database.TenantSalePayment
		s.db.Where("sale_id IN ?", saleIDs).Order("created_at ASC").Find(&payments)
		reportAmounts := buildSalePaymentReportAmountsFromPayments(salesMap, payments)
		paidBySale := make(map[uint]float64)
		for _, p := range payments {
			meth := normalizeReportMethod(p.Method)
			sale := salesMap[p.SaleID]
			if IsDetractionPaymentMethod(meth) || IsDetractionPaymentMethod(p.Method) {
				// La detracción SPOT no tiene un concepto de "Caja del pago" propio — el cliente
				// deposita directo a SUNAT, no hay cajero de por medio — así que se mantiene
				// atada a la Caja de registro de la venta, sin cambios.
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
			if paymentcondition.IsCreditCode(meth) || paymentcondition.IsCreditCode(p.Method) {
				// Marcador "credito" — no es dinero recibido, se ignora aquí a propósito (ver
				// bloque de "crédito generado" más abajo).
				continue
			}
			paidBySale[p.SaleID] += paymentReportAmount(p.Amount, p.ID, reportAmounts)
		}

		// Crédito generado: una sola pasada por cada venta a crédito de esta sesión (con o sin
		// marcador "credito", con o sin adelanto parcial ya cobrado) — siempre
		// sale.Total - paidBySale[sale.ID] (pagado en CUALQUIER Caja), nunca el monto crudo de
		// una fila individual de tenant_sale_payments. isCreditSale revisa tanto
		// PaymentConditionCode (confiable incluso con adelanto, donde PaymentMethod ya no es
		// "credito" sino el método real del adelanto) como PaymentMethod (compatibilidad con
		// datos históricos sin PaymentConditionCode).
		for _, sale := range sessionSales {
			isCreditSale := paymentcondition.IsCreditCode(sale.PaymentConditionCode) ||
				paymentcondition.IsCreditCode(sale.PaymentMethod)
			if !isCreditSale {
				continue
			}
			pending := money.RoundDisplay(sale.Total - paidBySale[sale.ID])
			if pending <= money.PaymentTolerance {
				continue
			}
			report.CreditGenerated.Total += pending
			report.CreditGenerated.Sales = append(report.CreditGenerated.Sales, IncomeDetailRow{
				Date:          sale.CreatedAt,
				Type:          "credito_generado",
				DocNumber:     displayDocNumbers[sale.ID],
				Amount:        pending,
				PaymentMethod: "credito",
			})
		}

		// Ventas legacy SIN ninguna fila en tenant_sale_payments (no hay ningún pago del cual
		// leer una Caja propia — es la única fuente disponible en ese caso): sigue atribuyendo
		// sale.Total al método de la propia venta, en la Caja de registro. No aplica a ventas a
		// crédito (ya resueltas arriba).
		for _, sale := range sessionSales {
			isCreditSale := paymentcondition.IsCreditCode(sale.PaymentConditionCode) ||
				paymentcondition.IsCreditCode(sale.PaymentMethod)
			if isCreditSale {
				continue
			}
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

	// (2) Dinero REAL recibido EN ESTA SESIÓN: TenantSalePayment.CashSessionID = sessionID, sin
	// importar en qué Caja se registró la venta a la que pertenece cada pago. Esto alimenta
	// IncomeDetail/TotalSales/TotalsByMethod.Sales/Electronic — la única fuente para "dinero
	// recibido", separada de "ventas registradas" (bloque de arriba). Cubre por igual venta
	// contado, adelanto de venta crédito y cobro posterior de CxC — los tres son, para este
	// bloque, simplemente "un TenantSalePayment cuya Caja es esta sesión".
	var paymentsReceivedHere []database.TenantSalePayment
	if err := s.db.Where("cash_session_id = ?", sessionID).Order("created_at ASC").Find(&paymentsReceivedHere).Error; err != nil {
		return nil, err
	}
	if len(paymentsReceivedHere) > 0 {
		missing := make([]uint, 0)
		seenNeeded := make(map[uint]struct{}, len(paymentsReceivedHere))
		for _, p := range paymentsReceivedHere {
			if _, ok := seenNeeded[p.SaleID]; ok {
				continue
			}
			seenNeeded[p.SaleID] = struct{}{}
			if _, known := salesMap[p.SaleID]; !known {
				missing = append(missing, p.SaleID)
			}
		}
		if len(missing) > 0 {
			// Pagos de ventas registradas en OTRA Caja (cobro posterior de CxC) — sus ventas no
			// están en sessionSales (esa lista es "registradas AQUÍ"), hay que resolverlas aparte
			// para poder mostrar el documento y detectar que son un cobro, no una venta directa.
			var extraSales []database.TenantSale
			s.db.Where("id IN ?", missing).Find(&extraSales)
			for _, es := range extraSales {
				salesMap[es.ID] = es
			}
			for id, num := range nvdisplay.LoadDisplayNumbersBySaleID(s.db, missing) {
				displayDocNumbers[id] = num
			}
		}
		receivedReportAmounts := buildSalePaymentReportAmountsFromPayments(salesMap, paymentsReceivedHere)
		for _, p := range paymentsReceivedHere {
			meth := normalizeReportMethod(p.Method)
			if IsDetractionPaymentMethod(meth) || IsDetractionPaymentMethod(p.Method) {
				continue // la detracción se reporta en (1), atada a la Caja de registro
			}
			if paymentcondition.IsCreditCode(meth) || paymentcondition.IsCreditCode(p.Method) {
				continue // el marcador "credito" no debería tener Caja propia, pero por si acaso
			}
			sale, saleKnown := salesMap[p.SaleID]
			reportAmt := paymentReportAmount(p.Amount, p.ID, receivedReportAmounts)
			salesByMethod[meth] += reportAmt
			report.Totals.TotalSales += reportAmt
			report.Totals.TotalSalesDirect += reportAmt
			report.Totals.TotalSalesCommercial += reportAmt
			row := IncomeDetailRow{
				Date:          p.CreatedAt,
				Type:          "venta",
				DocNumber:     displayDocNumbers[p.SaleID],
				Reference:     p.Reference,
				Amount:        reportAmt,
				PaymentMethod: meth,
			}
			if saleKnown && sale.CashSessionID != nil && *sale.CashSessionID != sessionID {
				// Cobro posterior de CxC: la venta se registró en OTRA Caja. Se distingue del
				// contado normal (mismo tratamiento que "pago_proveedor" vs "compra" en CxP) y
				// se deja trazabilidad hacia la Caja de registro original.
				row.Type = "cobro_cxc"
				origin := *sale.CashSessionID
				row.SaleCashSessionID = &origin
			}
			report.IncomeDetail = append(report.IncomeDetail, row)
		}
	}

	for _, m := range movements {
		paymentMethod := normalizeReportMethod(m.PaymentMethod)

		if m.Type == "income" {
			// TotalIncome/TotalExpense/FinalBalance alimentan CashPhysical.PhysicalBalance más
			// abajo, que se muestra como "saldo físico esperado en efectivo" (Excel, PDF, resumen
			// en pantalla). tenant_cash_movements también registra movimientos manuales por Yape/
			// Plin/transferencia (para trazabilidad de sesión), así que sin este filtro un egreso o
			// ingreso no-efectivo inflaba o desinflaba el "físico" sin haber tocado el cajón. Mismo
			// criterio que ya usa cashOnlyMovementTotals/getExpectedBalance (el cálculo real de
			// cierre/arqueo): IsCashPaymentMethod. El detalle (IncomeDetail/ExpenseDetail,
			// manualIncomeByMethod/manualExpenseByMethod, TotalsByMethod.Movements) no cambia — los
			// medios no-efectivo se siguen listando igual, solo se excluyen de este total físico.
			if IsCashPaymentMethod(paymentMethod) {
				report.Totals.TotalIncome += m.Amount
			}
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
			if IsCashPaymentMethod(paymentMethod) {
				report.Totals.TotalExpense += m.Amount
			}
			row := ExpenseDetailRow{Date: m.CreatedAt, Amount: m.Amount, PaymentMethod: paymentMethod}
			if m.PurchaseID != nil {
				report.Totals.TotalPurchases += m.Amount
				purchasesByMethod[paymentMethod] += m.Amount
				seenPurchaseIDs[*m.PurchaseID] = struct{}{}
				// compra (pago inmediato al registrar) vs pago_proveedor (CxP — Fase 2): Decisión A
				// prohíbe la compra mixta (purchase_service.Create: o paga todo de una vez o crea
				// el payable completo, nunca ambos), así que basta con el PaymentMethod de la
				// propia compra para clasificar sin ambigüedad cualquier movimiento con su
				// PurchaseID — no hace falta un campo nuevo.
				row.Type = "pago_proveedor"
				var pur database.TenantPurchase
				if s.db.First(&pur, *m.PurchaseID).Error == nil {
					row.DocNumber = pur.Series + "-" + pur.Number
					if strings.TrimSpace(pur.PaymentMethod) != "" {
						row.Type = "compra"
					}
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

	// Ingresos/egresos MANUALES no efectivo (Yape/Plin/transferencia/tarjeta): AddMovement ya no
	// duplica un TenantCashMovement para estos — viven exclusivamente en tenant_bank_movements
	// (sale_id/purchase_id nulos), así que sin este paso quedaban invisibles en IncomeDetail/
	// ExpenseDetail/TotalsByMethod.Movements pese a existir el movimiento bancario real. Nunca
	// suman a TotalIncome/TotalExpense/CashPhysical.PhysicalBalance: por ser no efectivo, el
	// mismo criterio (IsCashPaymentMethod) que ya excluye a los demás medios no-efectivo de esos
	// totales los excluye aquí también — solo aparecen en el detalle y en Movements.
	var manualBankMovs []database.TenantBankMovement
	s.db.Where("cash_session_id = ? AND sale_id IS NULL AND purchase_id IS NULL", sessionID).Find(&manualBankMovs)
	if len(manualBankMovs) > 0 {
		methodByAccount := s.paymentMethodCodesByBankAccount(manualBankMovs)
		for _, m := range manualBankMovs {
			code := methodByAccount[m.BankAccountID]
			if code == "" {
				// Cuenta sin método de pago vinculado (ni por FK ni por texto legado): mismo
				// criterio que paymentMethodCodesByBankAccount en GetSessionBalanceSummary — no
				// se cuela como "efectivo" (normalizeReportMethod trata "" así, pensado para
				// tenant_cash_movements sin dato, no para este caso).
				code = "otros"
			}
			method := normalizeReportMethod(code)
			if m.Type == "credit" {
				row := IncomeDetailRow{Date: m.CreatedAt, Amount: m.Amount, PaymentMethod: method, Reference: m.Reference}
				if m.Category == "ingreso_manual" || m.Category == "Ingreso manual" {
					row.Type = "ingreso_manual"
				} else {
					row.Type = "otro"
				}
				manualIncomeByMethod[method] += m.Amount
				report.IncomeDetail = append(report.IncomeDetail, row)
			} else {
				row := ExpenseDetailRow{Date: m.CreatedAt, Amount: m.Amount, PaymentMethod: method, Reference: m.Reference}
				if m.Category == "gasto" || m.Category == "Gasto" {
					row.Type = "gasto"
				} else {
					row.Type = "egreso_manual"
				}
				manualExpenseByMethod[method] += m.Amount
				report.ExpenseDetail = append(report.ExpenseDetail, row)
			}
		}
	}

	// Compras NO-EFECTIVO pagadas al contado: nunca pasan por tenant_cash_movements (arriba), así
	// que sin este paso quedaban invisibles en ExpenseDetail/TotalsByMethod.Purchases pese a
	// existir en tenant_purchases. Se leen directo de tenant_purchases (mismo dato que ya usa el
	// camino en efectivo vía pur.Series/pur.Number/m.Amount, sin acudir a tenant_bank_movements) y
	// solo se suman a TotalPurchases — nunca a TotalExpense/FinalBalance/CashPhysical
	// .PhysicalBalance, que deben seguir siendo solo efectivo. seenPurchaseIDs evita sumar dos
	// veces una compra que ya se haya contado arriba. Las compras a crédito (PaymentMethod vacío)
	// nunca aparecen aquí — IsCashPaymentMethod("") es true (ver normalizeReportMethod), así que
	// el filtro de esa función ya las excluye; se tratan aparte, más abajo (CxP — Fase 2).
	nonCashPurchases, _ := s.listNonCashPurchasesForSession(&session)
	for _, p := range nonCashPurchases {
		if _, ok := seenPurchaseIDs[p.ID]; ok {
			continue
		}
		method := normalizeReportMethod(p.PaymentMethod)
		report.Totals.TotalPurchases += p.Total
		purchasesByMethod[method] += p.Total
		report.ExpenseDetail = append(report.ExpenseDetail, ExpenseDetailRow{
			Date:          p.CreatedAt,
			Type:          "compra",
			DocNumber:     p.Series + "-" + p.Number,
			Amount:        p.Total,
			PaymentMethod: method,
		})
	}

	// CxP (Fase 2) — dos fuentes, análogas a CreditGenerated/pagos posteriores de CxC:
	//  1. Compras a crédito REGISTRADAS en esta sesión (purchase.cash_session_id = sessionID):
	//     no representan dinero pagado — se informan aparte en PayableGenerated, nunca en
	//     TotalPurchases/ExpenseDetail/TotalExpense.
	//  2. Pagos a proveedor NO-EFECTIVO que OCURRIERON en esta sesión (TenantPurchasePayment
	//     .cash_session_id = sessionID), que puede ser una sesión distinta de aquella en la que
	//     se registró la compra (P0: el pago tiene su propia Caja). Los pagos en efectivo no
	//     necesitan este paso: ya generan su propio TenantCashMovement, capturado arriba.
	creditPurchases, _ := s.listCreditPurchasesRegisteredInSession(&session)
	for _, p := range creditPurchases {
		report.PayableGenerated.Total += p.Total
		report.PayableGenerated.Purchases = append(report.PayableGenerated.Purchases, ExpenseDetailRow{
			Date:      p.CreatedAt,
			Type:      "cxp_generada",
			DocNumber: p.Series + "-" + p.Number,
			Amount:    p.Total,
		})
	}

	payablePayments, _ := s.listNonCashPayablePaymentsForSession(sessionID)
	for _, pp := range payablePayments {
		method := normalizeReportMethod(pp.Method)
		report.Totals.TotalPurchases += pp.Amount
		purchasesByMethod[method] += pp.Amount
		report.ExpenseDetail = append(report.ExpenseDetail, ExpenseDetailRow{
			Date:          pp.CreatedAt,
			Type:          "pago_proveedor",
			DocNumber:     pp.PurchaseDocNumber,
			Amount:        pp.Amount,
			PaymentMethod: method,
		})
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
	ensureSessionReportLists(report)
	return report, nil
}

// ensureSessionReportLists deja en [] toda lista que haya quedado nil.
//
// Un slice nil se serializa como null, no como [], así que una sesión sin movimientos devolvía
// "income_detail": null y el front reventaba con «Cannot read properties of null (reading
// 'filter')» al entrar al detalle. Pasó con la sesión 5 de tukifac, abierta y sin un solo
// movimiento. Varios consumidores ya se defendían con `?? []` uno por uno; el arreglo de fondo es
// que el contrato no mienta: si el campo es una lista, siempre llega una lista.
func ensureSessionReportLists(r *SessionReport) {
	if r.IncomeDetail == nil {
		r.IncomeDetail = []IncomeDetailRow{}
	}
	if r.ExpenseDetail == nil {
		r.ExpenseDetail = []ExpenseDetailRow{}
	}
	if r.CancelledSalesDetail == nil {
		r.CancelledSalesDetail = []CancelledSaleRow{}
	}
	if r.TotalsByMethod.Sales == nil {
		r.TotalsByMethod.Sales = []MethodTotal{}
	}
	if r.TotalsByMethod.Purchases == nil {
		r.TotalsByMethod.Purchases = []MethodTotal{}
	}
	if r.TotalsByMethod.Movements == nil {
		r.TotalsByMethod.Movements = []MethodTotal{}
	}
	if r.CashPhysical.CashSales == nil {
		r.CashPhysical.CashSales = []IncomeDetailRow{}
	}
	if r.CashPhysical.ManualIncome == nil {
		r.CashPhysical.ManualIncome = []IncomeDetailRow{}
	}
	if r.CashPhysical.Expenses == nil {
		r.CashPhysical.Expenses = []ExpenseDetailRow{}
	}
	if r.Electronic.SalesByMethod == nil {
		r.Electronic.SalesByMethod = []MethodTotal{}
	}
	if r.Electronic.Sales == nil {
		r.Electronic.Sales = []IncomeDetailRow{}
	}
	if r.Detraction.Sales == nil {
		r.Detraction.Sales = []IncomeDetailRow{}
	}
	if r.CreditGenerated.Sales == nil {
		r.CreditGenerated.Sales = []IncomeDetailRow{}
	}
	if r.PayableGenerated.Purchases == nil {
		r.PayableGenerated.Purchases = []ExpenseDetailRow{}
	}
}

func populateSessionReportSections(r *SessionReport) {
	cashSales := make([]IncomeDetailRow, 0)
	electronicSales := make([]IncomeDetailRow, 0)
	manualIncome := make([]IncomeDetailRow, 0)
	electronicByMethod := make(map[string]float64)
	var cashSalesTotal, electronicTotal float64

	for _, row := range r.IncomeDetail {
		switch row.Type {
		case "venta", "cobro_cxc":
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
	purchaseRows, err := s.buildPurchasePaymentMovementRows(f)
	if err != nil {
		return MovementsReportSplit{}, err
	}
	manualBankRows, err := s.buildManualBankMovementRows(f)
	if err != nil {
		return MovementsReportSplit{}, err
	}

	all := append(saleRows, cashRows...)
	all = append(all, cancelledElectronicRows...)
	all = append(all, purchaseRows...)
	all = append(all, manualBankRows...)
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
	// doc_type NOT IN NoteDocTypes: una nota de crédito/débito no tiene cash_session_id (se crea
	// aparte de SaleService.Create, ver CreateCreditNoteAndVoidSale) y cae dentro de esta misma
	// ventana de fecha/sucursal — sin este filtro, esta consulta la trae como si fuera una venta
	// más («huérfana») y el arqueo la suma como ingreso nuevo con el método de pago de la venta
	// que en realidad está anulando.
	q := salescope.CommercialSales(s.db.Model(&database.TenantSale{})).
		Where("(cash_session_id IS NULL OR cash_session_id = 0)").
		Where("branch_id = ?", session.BranchID).
		Where("created_at >= ?", session.OpenedAt).
		Where("status NOT IN ?", []string{"cancelled", "draft"}).
		Where("doc_type NOT IN ?", salescope.NoteDocTypes)
	if session.ClosedAt != nil {
		q = q.Where("created_at <= ?", *session.ClosedAt)
	}
	var sales []database.TenantSale
	err := q.Order("created_at ASC").Find(&sales).Error
	return sales, err
}

// listNonCashPurchasesForSession compras NO-EFECTIVO (Yape/Plin/transferencia/tarjeta) de la
// sesión, para completar ExpenseDetail/TotalsByMethod.Purchases — que hasta ahora solo se armaban
// leyendo tenant_cash_movements (donde una compra no-efectivo nunca aparece: recordDirectedPayment,
// rama bank_account, solo crea un TenantBankMovement, nunca un TenantCashMovement).
//
// Junta dos fuentes, en orden de preferencia:
//
//  1. VÍNCULO DIRECTO (cash_session_id = esta sesión) — el mecanismo real y determinístico para
//     compras NUEVAS. purchase_service.Create ahora exige y resuelve sesión de caja para
//     cualquier compra con pago inmediato, sin importar el método (ResolveCashSessionForPurchase,
//     mismo criterio que ResolveCashSessionForSale ya usa para ventas: la sesión abierta del
//     propio usuario, nunca "la primera sesión de la sucursal"). Una compra nueva por
//     Yape/Plin/transferencia/tarjeta ya llega aquí con esta columna poblada.
//  2. HUÉRFANAS (cash_session_id NULL), atribuidas por sucursal + ventana de fecha de la sesión —
//     SOLO COMPATIBILIDAD HISTÓRICA. Cubre exclusivamente compras creadas ANTES de que
//     purchase_service.Create exigiera sesión para pagos no-efectivo (dato ya persistido, no se
//     migra ni se corrige retroactivamente). Para una compra nueva con método de pago, esta rama
//     no debería aportar nada — si una compra nueva con pago inmediato aparece solo por esta vía,
//     es indicio de un bug en la resolución de sesión, no el comportamiento esperado. (Una compra
//     nueva SIN método de pago — a crédito, sin pago inmediato — legítimamente no pasa por
//     ResolveCashSessionForPurchase y por tanto tampoco por esta rama: normalizeReportMethod("")
//     la trata como "efectivo" y el filtro de abajo la descarta, ya que no representa ningún
//     movimiento de dinero que trazar todavía.)
//
// Las compras en EFECTIVO se excluyen explícitamente (IsCashPaymentMethod) de ambas fuentes: esas
// ya tienen su propio TenantCashMovement y ya se cuentan más arriba — incluirlas aquí las
// duplicaría.
func (s *CashBankService) listNonCashPurchasesForSession(session *database.TenantCashSession) ([]database.TenantPurchase, error) {
	if session == nil || session.ID == 0 {
		return nil, nil
	}
	var linked []database.TenantPurchase
	if err := s.db.Where("cash_session_id = ? AND status != ?", session.ID, "cancelled").
		Order("created_at ASC").Find(&linked).Error; err != nil {
		return nil, err
	}

	orphanQ := s.db.Model(&database.TenantPurchase{}).
		Where("(cash_session_id IS NULL OR cash_session_id = 0)").
		Where("branch_id = ?", session.BranchID).
		Where("created_at >= ?", session.OpenedAt).
		Where("status != ?", "cancelled")
	if session.ClosedAt != nil {
		orphanQ = orphanQ.Where("created_at <= ?", *session.ClosedAt)
	}
	var orphans []database.TenantPurchase
	if err := orphanQ.Order("created_at ASC").Find(&orphans).Error; err != nil {
		return nil, err
	}

	seen := make(map[uint]struct{}, len(linked))
	for _, p := range linked {
		seen[p.ID] = struct{}{}
	}
	all := linked
	for _, p := range orphans {
		if _, ok := seen[p.ID]; ok {
			continue
		}
		all = append(all, p)
	}

	out := make([]database.TenantPurchase, 0, len(all))
	for _, p := range all {
		if IsCashPaymentMethod(normalizeReportMethod(p.PaymentMethod)) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// listCreditPurchasesRegisteredInSession compras a crédito (CxP — Fase 2, PaymentMethod vacío)
// cuyo documento se REGISTRÓ en esta sesión (purchase.cash_session_id = session.ID) — vínculo
// directo únicamente: una compra a crédito nueva siempre lo tiene (ResolveCashSessionForPurchase
// ya lo exige desde P0), no hace falta un fallback huérfano por sucursal+fecha para esto.
func (s *CashBankService) listCreditPurchasesRegisteredInSession(session *database.TenantCashSession) ([]database.TenantPurchase, error) {
	if session == nil || session.ID == 0 {
		return nil, nil
	}
	var purchases []database.TenantPurchase
	err := s.db.Where("cash_session_id = ? AND status != ? AND (payment_method IS NULL OR payment_method = ?)",
		session.ID, "cancelled", "").
		Order("created_at ASC").Find(&purchases).Error
	return purchases, err
}

// payablePaymentRow pago a proveedor (TenantPurchasePayment) no-efectivo, con el número de
// documento de la compra ya resuelto (evita otra consulta por fila en el llamador).
type payablePaymentRow struct {
	Amount            float64
	Method            string
	CreatedAt         time.Time
	PurchaseDocNumber string
}

// listNonCashPayablePaymentsForSession pagos a proveedor NO-EFECTIVO (CxP — Fase 2) que
// OCURRIERON en esta sesión — TenantPurchasePayment.cash_session_id = sessionID, que puede ser
// una sesión distinta de aquella en la que se registró la compra (P0: el pago tiene su propia
// Caja, independiente de la del documento). Los pagos en efectivo no pasan por aquí: ya generan
// su propio TenantCashMovement, capturado por el loop principal de movimientos más arriba —
// incluirlos aquí también los duplicaría.
func (s *CashBankService) listNonCashPayablePaymentsForSession(sessionID uint) ([]payablePaymentRow, error) {
	if sessionID == 0 {
		return nil, nil
	}
	var payments []database.TenantPurchasePayment
	if err := s.db.Where("cash_session_id = ?", sessionID).Order("created_at ASC").Find(&payments).Error; err != nil {
		return nil, err
	}
	out := make([]payablePaymentRow, 0, len(payments))
	for _, p := range payments {
		if IsCashPaymentMethod(normalizeReportMethod(p.Method)) {
			continue
		}
		docNumber := ""
		var pur database.TenantPurchase
		if s.db.First(&pur, p.PurchaseID).Error == nil {
			docNumber = pur.Series + "-" + pur.Number
		}
		out = append(out, payablePaymentRow{
			Amount: p.Amount, Method: p.Method, CreatedAt: p.CreatedAt, PurchaseDocNumber: docNumber,
		})
	}
	return out, nil
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
		// cash_session_id viene del PAGO (TenantSalePayment.CashSessionID), nunca de la venta: un
		// cobro posterior en otra Caja debe aparecer en la Caja donde realmente se recibió, no en
		// la Caja donde se registró el documento (P0). COALESCE a 0 para pagos históricos sin
		// backfill (el 0 es el mismo centinela "sin sesión" que ya usa el resto del código, p.ej.
		// "tenant_sales.cash_session_id > 0" un poco más abajo) — el frontend ya lo muestra como
		// "—". El JOIN a tenant_cash_sessions sigue siendo el de la venta: solo resuelve la
		// sucursal para el filtro f.BranchID, no el cash_session_id de salida.
		Select(`tenant_sale_payments.id AS payment_id, tenant_sale_payments.sale_id, tenant_sale_payments.method,
			tenant_sale_payments.amount, tenant_sale_payments.reference, tenant_sale_payments.notes, tenant_sale_payments.created_at,
			tenant_sales.number AS sale_number, tenant_sales.user_id AS sale_user_id, tenant_sales.contact_id,
			COALESCE(tenant_sale_payments.cash_session_id, 0) AS cash_session_id, tenant_sales.payment_method AS sale_payment_method, tenant_sales.created_at AS sale_created_at,
			tenant_cash_sessions.branch_id`).
		Joins("JOIN tenant_sales ON tenant_sales.id = tenant_sale_payments.sale_id").
		Joins("JOIN tenant_cash_sessions ON tenant_cash_sessions.id = tenant_sales.cash_session_id").
		Where("tenant_sales.cash_session_id > 0").
		Where("tenant_sales.status NOT IN ?", []string{"cancelled", "draft"})

	if f.SessionID > 0 {
		// Filtra por la Caja donde ocurrió el PAGO, no donde se registró la venta — así un cobro
		// posterior de una venta registrada en otra Caja aparece al pedir el reporte de la Caja
		// donde realmente se cobró (antes: "tenant_sales.cash_session_id = f.SessionID" excluía
		// por completo cualquier cobro de una venta registrada en otra sesión).
		q = q.Where("tenant_sale_payments.cash_session_id = ?", f.SessionID)
	}
	if f.BranchID > 0 {
		q = q.Where("tenant_cash_sessions.branch_id = ?", f.BranchID)
	}
	if f.UserID > 0 {
		q = q.Where("tenant_sales.user_id = ?", f.UserID)
	}
	if f.DateFrom != nil {
		q = q.Where("tenant_sale_payments.created_at >= ?", f.DateFrom)
	}
	if f.DateTo != nil {
		q = q.Where("tenant_sale_payments.created_at <= ?", f.DateTo)
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
		if paymentcondition.IsCreditCode(p.Method) {
			// Marcador "credito" (venta 100% a crédito sin adelanto, ver sale_service.Create) —
			// no es dinero recibido. Sin este filtro terminaba contado como venta electrónica
			// real en el canal "electronic" de este reporte multisesión (movementRowChannel solo
			// mira el método, no el tipo de fila).
			continue
		}
		meth := normalizeReportMethod(p.Method)
		contactName := ""
		if p.ContactID != nil {
			contactName = contacts[*p.ContactID]
		}
		docNum := displayDocNumbers[p.SaleID]
		if docNum == "" {
			docNum = p.SaleNumber
		}
		saleID := p.SaleID
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
			SaleID:        &saleID,
		})
	}

	// Ventas legacy sin líneas en tenant_sale_payments
	for _, sale := range legacySales {
		if sale.CashSessionID == nil {
			continue
		}
		if paymentcondition.IsCreditCode(sale.PaymentConditionCode) || paymentcondition.IsCreditCode(sale.PaymentMethod) {
			// Venta a crédito sin ninguna fila en tenant_sale_payments (ni siquiera el marcador
			// "credito") — no hubo ningún cobro real que listar aquí. Mismo criterio que arriba.
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
		saleID := sale.ID
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
			SaleID:        &saleID,
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
	contactIDs := make(map[uint]struct{})
	for _, m := range movements {
		sessionIDs[m.CashSessionID] = struct{}{}
		userIDs[m.UserID] = struct{}{}
		if m.PurchaseID != nil {
			purchaseIDs[*m.PurchaseID] = struct{}{}
		}
		if m.ContactID != nil {
			contactIDs[*m.ContactID] = struct{}{}
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
	contactNames := loadContactNamesMap(s.db, contactIDs)

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
			row.SaleID = m.SaleID
			var sale database.TenantSale
			if s.db.First(&sale, *m.SaleID).Error == nil {
				row.DocNumber = sale.Number
			}
		} else if m.PurchaseID != nil {
			// compra (pago inmediato) vs pago_proveedor (CxP): mismo criterio que GetSessionReport
			// — Decisión A prohíbe la compra mixta, así que el PaymentMethod de la propia compra
			// alcanza para clasificar sin ambigüedad.
			row.Type = "pago_proveedor"
			row.PurchaseID = m.PurchaseID
			if pur, ok := purchases[*m.PurchaseID]; ok {
				row.DocNumber = pur.Series + "-" + pur.Number
				row.ContactName = contactsByPurchase[*m.PurchaseID]
				if strings.TrimSpace(pur.PaymentMethod) != "" {
					row.Type = "compra"
				}
			}
		} else if m.Type == "income" {
			row.Type = "ingreso"
			row.DocNumber = m.Reference
			row.ContactName = m.Notes
		} else {
			row.Type = "egreso"
			row.DocNumber = m.Reference
			// Proveedor vinculado a mano (AddMovement, vista de Egresos) tiene prioridad sobre el
			// viejo criterio de usar las notas como "quién" — un egreso manual sin proveedor sigue
			// mostrando las notas, igual que siempre.
			if m.ContactID != nil {
				if name := contactNames[*m.ContactID]; name != "" {
					row.ContactName = name
				}
			}
			if row.ContactName == "" {
				row.ContactName = m.Notes
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// buildManualBankMovementRows movimientos MANUALES no efectivo (ingreso/egreso de Caja vía
// AddMovement, sin sale_id/purchase_id) para ListMovementsReport — el reporte multisesión.
// Desde el fix de la duplicación en AddMovement, estos viven EXCLUSIVAMENTE en
// tenant_bank_movements (antes generaban además un TenantCashMovement, que sí cubría
// buildCashMovementReportRows). Sin esto, un ingreso/egreso manual por Yape/Plin/tarjeta/
// transferencia desaparecía por completo de este reporte — mismo gap ya corregido en
// GetSessionReport (el reporte de UNA sesión), aquí extendido al reporte de VARIAS.
func (s *CashBankService) buildManualBankMovementRows(f MovementReportFilters) ([]MovementReportRow, error) {
	q := s.db.Model(&database.TenantBankMovement{}).
		Joins("JOIN tenant_cash_sessions ON tenant_cash_sessions.id = tenant_bank_movements.cash_session_id").
		Where("tenant_bank_movements.sale_id IS NULL AND tenant_bank_movements.purchase_id IS NULL").
		Where("tenant_bank_movements.cash_session_id IS NOT NULL")
	if f.SessionID > 0 {
		q = q.Where("tenant_bank_movements.cash_session_id = ?", f.SessionID)
	}
	if f.BranchID > 0 {
		q = q.Where("tenant_cash_sessions.branch_id = ?", f.BranchID)
	}
	if f.UserID > 0 {
		q = q.Where("tenant_bank_movements.user_id = ?", f.UserID)
	}
	if f.DateFrom != nil {
		q = q.Where("tenant_bank_movements.created_at >= ?", f.DateFrom)
	}
	if f.DateTo != nil {
		q = q.Where("tenant_bank_movements.created_at <= ?", f.DateTo)
	}
	// income/expense (el vocabulario del filtro) → credit/debit (el de tenant_bank_movements).
	if f.MovementType == "income" {
		q = q.Where("tenant_bank_movements.type = ?", "credit")
	} else if f.MovementType == "expense" {
		q = q.Where("tenant_bank_movements.type = ?", "debit")
	}

	var movements []database.TenantBankMovement
	if err := q.Order("tenant_bank_movements.created_at DESC").Find(&movements).Error; err != nil {
		return nil, fmt.Errorf("movimientos manuales no efectivo: %w", err)
	}
	if len(movements) == 0 {
		return nil, nil
	}

	// El método de pago no es una columna de tenant_bank_movements — se resuelve por cuenta,
	// igual que en GetMovements/GetSessionBalanceSummary, así que el filtro por método se aplica
	// después de traer las filas (no hay forma de empujarlo a SQL sin ese dato).
	methodByAccount := s.paymentMethodCodesByBankAccount(movements)
	wantMethod := ""
	if f.PaymentMethod != "" {
		wantMethod = normalizeReportMethod(f.PaymentMethod)
	}

	sessionIDs := make(map[uint]struct{})
	userIDs := make(map[uint]struct{})
	for _, m := range movements {
		if m.CashSessionID != nil {
			sessionIDs[*m.CashSessionID] = struct{}{}
		}
		userIDs[m.UserID] = struct{}{}
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
	contactIDs := make(map[uint]struct{})
	for _, m := range movements {
		if m.ContactID != nil {
			contactIDs[*m.ContactID] = struct{}{}
		}
	}
	contactNames := loadContactNamesMap(s.db, contactIDs)

	rows := make([]MovementReportRow, 0, len(movements))
	for _, m := range movements {
		code := methodByAccount[m.BankAccountID]
		if code == "" {
			code = "otros"
		}
		method := normalizeReportMethod(code)
		if wantMethod != "" && method != wantMethod {
			continue
		}
		amount := m.Amount
		typ := "ingreso"
		if m.Type == "debit" {
			amount = -amount
			typ = "egreso"
		}
		var sessionID uint
		var branchName string
		if m.CashSessionID != nil {
			sessionID = *m.CashSessionID
			if ses, ok := sessions[sessionID]; ok {
				branchName = branches[ses.BranchID]
			}
		}
		// Proveedor vinculado a mano (AddMovement, vista de Egresos) tiene prioridad sobre el
		// viejo criterio de usar las notas como "quién" — mismo criterio que buildCashMovementReportRows.
		contactName := m.Notes
		if m.ContactID != nil {
			if name := contactNames[*m.ContactID]; name != "" {
				contactName = name
			}
		}
		rows = append(rows, MovementReportRow{
			Date:          m.CreatedAt,
			Type:          typ,
			DocNumber:     m.Reference,
			ContactName:   contactName,
			UserName:      users[m.UserID],
			BranchName:    branchName,
			PaymentMethod: method,
			Amount:        amount,
			MovementID:    manualBankMovementID(m.ID),
			CashSessionID: sessionID,
			Category:      m.Category,
			CashReference: m.Reference,
			NotesDetail:   m.Notes,
		})
	}
	return rows, nil
}

// buildPurchasePaymentMovementRows movimientos NO-EFECTIVO de compras (contado o pago a
// proveedor/CxP) para ListMovementsReport — el reporte de movimientos multi-sesión, hasta ahora,
// solo traía compras EFECTIVO (vía buildCashMovementReportRows/tenant_cash_movements); un pago
// por Yape/Plin/tarjeta/transferencia de una compra o de un pago a proveedor nunca aparecía.
//
// Dos fuentes, sin unión con tenant_bank_movements (evita depender de que ese movimiento exista
// o de resolver su método vía BankAccountID — ver nota en la sesión-report de por qué esa cuenta
// no es un dato confiable para esto):
//  1. TenantPurchase con PaymentMethod != "" — el pago inmediato al registrar (mismo dato que ya
//     usa listNonCashPurchasesForSession para el reporte de una sesión).
//  2. TenantPurchasePayment — cualquier pago de CxP (Fase 2); PayableService.Pay es la ÚNICA
//     función que crea filas ahí, así que su sola existencia ya identifica un pago a proveedor.
//
// El efectivo de ambas categorías sigue viniendo de tenant_cash_movements (arriba): esta función
// filtra explícitamente !IsCashPaymentMethod en las dos fuentes para no duplicarlo.
func (s *CashBankService) buildPurchasePaymentMovementRows(f MovementReportFilters) ([]MovementReportRow, error) {
	if f.MovementType == "income" {
		return nil, nil // compras y pagos a proveedor siempre son egreso
	}

	rows := make([]MovementReportRow, 0, 16)

	// 1) Compra al contado, medio NO efectivo.
	pq := s.db.Model(&database.TenantPurchase{}).
		Joins("JOIN tenant_cash_sessions ON tenant_cash_sessions.id = tenant_purchases.cash_session_id").
		Where("tenant_purchases.payment_method IS NOT NULL AND tenant_purchases.payment_method != ''").
		Where("tenant_purchases.status != ?", "cancelled").
		Where("tenant_purchases.cash_session_id > 0")
	if f.SessionID > 0 {
		pq = pq.Where("tenant_purchases.cash_session_id = ?", f.SessionID)
	}
	if f.BranchID > 0 {
		pq = pq.Where("tenant_cash_sessions.branch_id = ?", f.BranchID)
	}
	if f.UserID > 0 {
		pq = pq.Where("tenant_purchases.user_id = ?", f.UserID)
	}
	if f.DateFrom != nil {
		pq = pq.Where("tenant_purchases.created_at >= ?", f.DateFrom)
	}
	if f.DateTo != nil {
		pq = pq.Where("tenant_purchases.created_at <= ?", f.DateTo)
	}
	if f.PaymentMethod != "" {
		pq = applyPaymentMethodFilter(pq, "tenant_purchases.payment_method", f.PaymentMethod)
	}
	var purchases []database.TenantPurchase
	if err := pq.Order("tenant_purchases.created_at DESC").Find(&purchases).Error; err != nil {
		return nil, fmt.Errorf("compras no-efectivo: %w", err)
	}

	contactIDs := make(map[uint]struct{})
	userIDs := make(map[uint]struct{})
	branchIDsByPurchaseSession := make(map[uint]uint) // cash_session_id -> branch_id, para no repetir el join
	for _, p := range purchases {
		if p.ContactID != nil {
			contactIDs[*p.ContactID] = struct{}{}
		}
		userIDs[p.UserID] = struct{}{}
	}

	for _, p := range purchases {
		method := normalizeReportMethod(p.PaymentMethod)
		if IsCashPaymentMethod(method) {
			continue // ya cubierto por tenant_cash_movements — evita duplicar
		}
		contactName := ""
		if p.ContactID != nil {
			var c database.TenantContact
			if s.db.First(&c, *p.ContactID).Error == nil {
				contactName = contactDisplayName(c)
			}
		}
		userName := ""
		var u database.TenantUser
		if s.db.First(&u, p.UserID).Error == nil {
			userName = u.Name
		}
		branchName := ""
		if bID, ok := branchIDsByPurchaseSession[*p.CashSessionID]; ok {
			branchName = loadBranchNamesMap(s.db, map[uint]struct{}{bID: {}})[bID]
		} else {
			var ses database.TenantCashSession
			if s.db.First(&ses, *p.CashSessionID).Error == nil {
				branchIDsByPurchaseSession[*p.CashSessionID] = ses.BranchID
				var b database.TenantBranch
				if s.db.First(&b, ses.BranchID).Error == nil {
					branchName = b.Name
				}
			}
		}
		purchaseID := p.ID
		rows = append(rows, MovementReportRow{
			Date:          p.CreatedAt,
			Type:          "compra",
			DocNumber:     p.Series + "-" + p.Number,
			ContactName:   contactName,
			UserName:      userName,
			BranchName:    branchName,
			PaymentMethod: method,
			Amount:        -p.Total,
			MovementID:    purchaseMovementID(p.ID),
			CashSessionID: *p.CashSessionID,
			Category:      "Compra",
			PurchaseID:    &purchaseID,
		})
	}

	// 2) Pago a proveedor (CxP), medio NO efectivo.
	ppq := s.db.Model(&database.TenantPurchasePayment{}).
		Joins("JOIN tenant_cash_sessions ON tenant_cash_sessions.id = tenant_purchase_payments.cash_session_id").
		Where("tenant_purchase_payments.cash_session_id > 0")
	if f.SessionID > 0 {
		ppq = ppq.Where("tenant_purchase_payments.cash_session_id = ?", f.SessionID)
	}
	if f.BranchID > 0 {
		ppq = ppq.Where("tenant_cash_sessions.branch_id = ?", f.BranchID)
	}
	if f.DateFrom != nil {
		ppq = ppq.Where("tenant_purchase_payments.created_at >= ?", f.DateFrom)
	}
	if f.DateTo != nil {
		ppq = ppq.Where("tenant_purchase_payments.created_at <= ?", f.DateTo)
	}
	if f.PaymentMethod != "" {
		ppq = applyPaymentMethodFilter(ppq, "tenant_purchase_payments.method", f.PaymentMethod)
	}
	// f.UserID: TenantPurchasePayment no guarda quién hizo el pago (solo su Caja) — no se puede
	// filtrar por usuario en esta fuente. Limitación conocida, documentada en el informe.
	var payments []database.TenantPurchasePayment
	if err := ppq.Order("tenant_purchase_payments.created_at DESC").Find(&payments).Error; err != nil {
		return nil, fmt.Errorf("pagos CxP no-efectivo: %w", err)
	}

	for _, pp := range payments {
		method := normalizeReportMethod(pp.Method)
		if IsCashPaymentMethod(method) {
			continue // ya cubierto por tenant_cash_movements — evita duplicar
		}
		docNumber, contactName := "", ""
		var pur database.TenantPurchase
		if s.db.First(&pur, pp.PurchaseID).Error == nil {
			docNumber = pur.Series + "-" + pur.Number
			if pur.ContactID != nil {
				var c database.TenantContact
				if s.db.First(&c, *pur.ContactID).Error == nil {
					contactName = contactDisplayName(c)
				}
			}
		}
		branchName := ""
		if bID, ok := branchIDsByPurchaseSession[*pp.CashSessionID]; ok {
			branchName = loadBranchNamesMap(s.db, map[uint]struct{}{bID: {}})[bID]
		} else {
			var ses database.TenantCashSession
			if s.db.First(&ses, *pp.CashSessionID).Error == nil {
				branchIDsByPurchaseSession[*pp.CashSessionID] = ses.BranchID
				var b database.TenantBranch
				if s.db.First(&b, ses.BranchID).Error == nil {
					branchName = b.Name
				}
			}
		}
		purchaseID := pp.PurchaseID
		rows = append(rows, MovementReportRow{
			Date:          pp.CreatedAt,
			Type:          "pago_proveedor",
			DocNumber:     docNumber,
			ContactName:   contactName,
			PaymentMethod: method,
			Amount:        -pp.Amount,
			MovementID:    purchasePaymentMovementID(pp.ID),
			CashSessionID: *pp.CashSessionID,
			Category:      "Pago proveedor",
			BranchName:    branchName,
			PurchaseID:    &purchaseID,
		})
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
		// El marcador "credito" no es dinero recibido — queda fuera de la base del prorrateo.
		// Incluirlo inflaba `sum` (líneas reales + marcador) muy por encima de sale.Total,
		// disparando la rama de "vuelto" de AllocateSalePaymentReportAmounts y repartiendo el
		// importe de la venta entre TODAS las líneas (incluido el marcador), en vez de asignarle
		// a cada pago real su propio monto tal cual — eso era lo que producía montos "a mitad"
		// en el reporte cuando una venta a crédito sin adelanto luego se cobraba en otra sesión.
		lines := make([]money.SalePaymentLine, 0, len(pList))
		for _, p := range pList {
			if paymentcondition.IsCreditCode(p.Method) {
				continue
			}
			lines = append(lines, money.SalePaymentLine{ID: p.ID, Amount: p.Amount})
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
