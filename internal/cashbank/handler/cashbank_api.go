package handler

import (
	"strconv"
	"time"

	"tukifac/internal/cashbank/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
)

// ══════════════════════════════════════════════
// CAJA — Sesiones
// ══════════════════════════════════════════════

// GET /api/cashbank/sessions?branch_id=&page=&per_page=
func (h *CashBankHandler) ListSessionsAPI(c fiber.Ctx) error {
	req, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(req))
	page, _ := strconv.Atoi(c.Query("page"))
	perPage, _ := strconv.Atoi(c.Query("per_page"))
	// callerUserIDOrZero: 0 si administra cualquier caja (ve todas), su propio user_id si no —
	// mismo criterio que antes aplicaba filterSessionsForCaller después de traer la página
	// completa, ahora filtrado en la propia consulta SQL (ver SessionListParams.OpenedBy), para
	// que el total y el offset de la paginación sean correctos también para quien no administra.
	params := service.SessionListParams{BranchID: branchID, OpenedBy: callerUserIDOrZero(c), Page: page, PerPage: perPage}
	sessions, total, err := service.NewCashBankService(db(c)).ListSessionsEnriched(params)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": sessions, "total": total})
}

// GET /api/cashbank/sessions/open/list?branch_id= — cajas abiertas en sucursal (solo lectura).
func (h *CashBankHandler) ListOpenSessionsInBranchAPI(c fiber.Ctx) error {
	req, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	// Valida acceso: una sucursal ajena no se lista (cae a la activa); sin sucursal resuelta falla cerrado.
	branchID := branch.ResolveReadBranchFilter(c, uint(req))
	if branchID == branch.NoBranchFilter {
		return c.Status(403).JSON(fiber.Map{"error": "Sucursal activa requerida", "code": branch.CodeBranchRequired})
	}
	list, err := service.NewCashBankService(db(c)).ListOpenSessionsInBranch(branchID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}

// GET /api/cashbank/sessions/open?branch_id= — sesión abierta del usuario en la sucursal indicada o activa en JWT.
func (h *CashBankHandler) GetOpenSessionAPI(c fiber.Ctx) error {
	req, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(req))
	if branchID == branch.NoBranchFilter {
		return c.Status(403).JSON(fiber.Map{"error": "Sucursal activa requerida", "code": branch.CodeBranchRequired})
	}
	uid := userID(c)
	session, err := service.NewCashBankService(db(c)).GetOpenSession(branchID, uid)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if session == nil {
		return c.JSON(fiber.Map{"data": nil, "open": false})
	}
	return c.JSON(fiber.Map{"data": session, "open": true})
}

// POST /api/cashbank/sessions
func (h *CashBankHandler) OpenSessionAPI(c fiber.Ctx) error {
	var body struct {
		BranchID       uint    `json:"branch_id"`
		OpeningBalance float64 `json:"opening_balance"`
		Notes          string  `json:"notes"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	sess, err := service.NewCashBankService(db(c)).OpenSession(service.OpenSessionInput{
		BranchID: branchID, UserID: userID(c), OpeningBalance: body.OpeningBalance, Notes: body.Notes,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true, "data": sess})
}

// POST /api/cashbank/sessions/:id/close
func (h *CashBankHandler) CloseSessionAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		ClosingBalance float64            `json:"closing_balance"`
		Notes          string             `json:"notes"`
		Arqueo         map[string]float64 `json:"arqueo"` // opcional: {"200":5,"100":10,...}
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	closeSess, _ := service.NewCashBankService(db(c)).GetSessionByID(uint(id))
	if denied, resp := sessionWriteDenied(c, closeSess); denied {
		return resp
	}
	allowAny := canManageAnyCashSession(c)
	if err := service.NewCashBankService(db(c)).CloseSession(uint(id), userID(c), body.ClosingBalance, body.Notes, body.Arqueo, allowAny); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/cashbank/sessions/:id — sesión con arqueo para modal
func (h *CashBankHandler) GetSessionAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewCashBankService(db(c))
	session, err := svc.GetSessionByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	if !sessionReadableInBranch(c, session) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Sesión no encontrada"})
	}
	if !canAccessCashSession(c, session) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "No puede ver esta sesión de caja"})
	}
	return c.JSON(fiber.Map{"data": session})
}

// POST /api/cashbank/sessions/:id/arqueo — guardar/actualizar arqueo (abierta: borrador; cerrada sin arqueo: registrar una vez)
func (h *CashBankHandler) SaveArqueoAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Arqueo map[string]float64 `json:"arqueo"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.Arqueo == nil {
		body.Arqueo = make(map[string]float64)
	}
	arqSess, _ := service.NewCashBankService(db(c)).GetSessionByID(uint(id))
	if denied, resp := sessionWriteDenied(c, arqSess); denied {
		return resp
	}
	sum, err := service.NewCashBankService(db(c)).SaveArqueo(uint(id), userID(c), body.Arqueo)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "sum": sum})
}

// GET /api/cashbank/sessions/:id/movements
func (h *CashBankHandler) GetMovementsAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewCashBankService(db(c))
	sess, err := svc.GetSessionByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	if !sessionReadableInBranch(c, sess) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Sesión no encontrada"})
	}
	if !canAccessCashSession(c, sess) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "No puede ver movimientos de esta sesión"})
	}
	movements, err := svc.GetMovements(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": movements})
}

// GetSessionBalanceAPI GET /api/cashbank/sessions/:id/balance — fuente única de saldo: totales
// por método de pago (efectivo, Yape, Plin, transferencia, tarjeta, otros) + total de la sesión +
// el efectivo esperado (mismo cálculo que usa el cierre/arqueo). El frontend consume esto en vez
// de sumar movimientos por su cuenta.
func (h *CashBankHandler) GetSessionBalanceAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewCashBankService(db(c))
	sess, err := svc.GetSessionByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	if !sessionReadableInBranch(c, sess) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Sesión no encontrada"})
	}
	if !canAccessCashSession(c, sess) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "No puede ver el saldo de esta sesión"})
	}
	summary, err := svc.GetSessionBalanceSummary(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": summary})
}

// ReverseMovementAPI POST /api/cashbank/movements/:id/reverse — revierte un movimiento MANUAL
// (ingreso/egreso sin venta/compra asociada). El original nunca se borra ni se modifica: se crea
// un movimiento nuevo de signo opuesto con reversal_of_id, trazable, dentro de la misma sesión.
func (h *CashBankHandler) ReverseMovementAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewCashBankService(db(c))
	var body struct {
		Notes string `json:"notes"`
		// Kind: "cash" (tenant_cash_movements) o "bank" (tenant_bank_movements) — GetMovements ya
		// devuelve este dato por fila (CashMovementView.Kind); el frontend lo reenvía tal cual, sin
		// adivinar, porque ambas tablas tienen su propia secuencia de IDs (un mismo número puede
		// existir en las dos). Vacío = "cash", por compatibilidad con un frontend aún no
		// actualizado — hasta ahora el único tipo que existía.
		Kind string `json:"kind"`
	}
	_ = c.Bind().Body(&body)
	kind := body.Kind
	if kind == "" {
		kind = "cash"
	}

	var sessionID uint
	switch kind {
	case "cash":
		var mov database.TenantCashMovement
		if err := db(c).First(&mov, uint(id)).Error; err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Movimiento no encontrado"})
		}
		sessionID = mov.CashSessionID
	case "bank":
		var mov database.TenantBankMovement
		if err := db(c).First(&mov, uint(id)).Error; err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Movimiento no encontrado"})
		}
		if mov.CashSessionID == nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Sesión de caja no encontrada"})
		}
		sessionID = *mov.CashSessionID
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Tipo de movimiento inválido"})
	}

	sess, err := svc.GetSessionByID(sessionID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Sesión de caja no encontrada"})
	}
	if denied, resp := sessionWriteDenied(c, sess); denied {
		return resp
	}
	if !canAccessCashSession(c, sess) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "No puede revertir movimientos de esta sesión"})
	}
	if err := svc.ReverseManualMovement(db(c), kind, uint(id), userID(c), body.Notes); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/cashbank/sessions/:id/movements
func (h *CashBankHandler) AddMovementAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Type          string  `json:"type"` // income | expense
		Category      string  `json:"category"`
		Reference     string  `json:"reference"`
		PaymentMethod string  `json:"payment_method"`
		Amount        float64 `json:"amount"`
		Notes         string  `json:"notes"`
		// ContactID: proveedor/cliente vinculado (opcional) — típico en un egreso a proveedor sin
		// compra registrada todavía (frontend: CashMovementTypeView.tsx, vista de Egresos).
		ContactID *uint `json:"contact_id"`
		// DestinationAccountID: cuenta que recibe (ingreso) o de la que sale (egreso) el dinero; nil = la cuenta del método. No aplica al efectivo.
		DestinationAccountID *uint `json:"destination_account_id"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	svc := service.NewCashBankService(db(c))
	mvSess, _ := svc.GetSessionByID(uint(id))
	if denied, resp := sessionWriteDenied(c, mvSess); denied {
		return resp
	}
	if _, err := svc.ValidateCashSessionForUser(uint(id), userID(c), branch.ActiveBranchID(c)); err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	if err := svc.AddMovementToAccount(
		uint(id), userID(c), body.Type, body.Category, body.Reference, body.PaymentMethod, body.Amount, body.Notes, body.ContactID, body.DestinationAccountID,
	); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true})
}

// ══════════════════════════════════════════════
// REPORTES DE CAJA
// ══════════════════════════════════════════════

// GET /api/cashbank/sessions/:id/report
func (h *CashBankHandler) GetSessionReportAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewCashBankService(db(c))
	sess, err := svc.GetSessionByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	if !sessionReadableInBranch(c, sess) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Sesión no encontrada"})
	}
	if !canAccessCashSession(c, sess) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "No puede ver el reporte de esta sesión"})
	}
	report, err := svc.GetSessionReport(uint(id))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": report})
}

// GET /api/cashbank/sessions/:id/report/products
func (h *CashBankHandler) GetSessionProductsReportAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewCashBankService(db(c))
	sess, err := svc.GetSessionByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	if !sessionReadableInBranch(c, sess) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Sesión no encontrada"})
	}
	if !canAccessCashSession(c, sess) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "No puede ver el reporte de esta sesión"})
	}
	rows, err := svc.GetSessionProductsReport(uint(id))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// GET /api/cashbank/sessions/:id/report/documents — comprobantes (vigentes y anulados) de la sesión,
// con subtotales por tipo; alimenta el «Detalle de caja» imprimible.
func (h *CashBankHandler) GetSessionDocumentsReportAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewCashBankService(db(c))
	sess, err := svc.GetSessionByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	if !sessionReadableInBranch(c, sess) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Sesión no encontrada"})
	}
	if !canAccessCashSession(c, sess) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "No puede ver el reporte de esta sesión"})
	}
	rep, err := svc.GetSessionDocumentsReport(uint(id))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rep})
}

// GET /api/cashbank/reports/movements?branch_id=&user_id=&date_from=&date_to=&session_id=&type=&payment_method=&page=&per_page=
// per_page=0 u omitido: todas las filas (compatibilidad con vistas que agrupan totales).
func (h *CashBankHandler) ListMovementsReportAPI(c fiber.Ctx) error {
	var f service.MovementReportFilters
	if v, err := strconv.ParseUint(c.Query("branch_id"), 10, 32); err == nil {
		f.BranchID = branch.ResolveReadBranchFilter(c, uint(v))
	} else {
		f.BranchID = branch.ResolveReadBranchFilter(c, 0)
	}
	if scoped := callerUserIDOrZero(c); scoped > 0 {
		f.UserID = scoped
	} else if v, err := strconv.ParseUint(c.Query("user_id"), 10, 32); err == nil {
		f.UserID = uint(v)
	}
	if v, err := strconv.ParseUint(c.Query("session_id"), 10, 32); err == nil {
		f.SessionID = uint(v)
		// Una sesión de otra sucursal (no accesible) o ajena a un cajero se responde como inexistente.
		rs, serr := service.NewCashBankService(db(c)).GetSessionByID(f.SessionID)
		if serr != nil || !sessionReadableInBranch(c, rs) || !canAccessCashSession(c, rs) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Sesión no encontrada"})
		}
	}
	f.MovementType = c.Query("type")
	f.PaymentMethod = c.Query("payment_method")
	if df := c.Query("date_from"); df != "" {
		if t, err := time.ParseInLocation("2006-01-02", df, time.Local); err == nil {
			start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
			f.DateFrom = &start
		}
	}
	if dt := c.Query("date_to"); dt != "" {
		if t, err := time.ParseInLocation("2006-01-02", dt, time.Local); err == nil {
			end := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.Local)
			f.DateTo = &end
		}
	}

	perPage := 0
	if v := c.Query("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			perPage = n
		}
	}
	page := 1
	if v := c.Query("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	// Con sesión seleccionada, alinear con el reporte de cierre (sin recorte por fechas del mes).
	if f.SessionID > 0 {
		f.DateFrom = nil
		f.DateTo = nil
	}

	split, err := service.NewCashBankService(db(c)).ListMovementsReport(f)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	// Compatibilidad: clientes legacy esperan data/total/summary plano (paginado aparte).
	legacyAll := append(append([]service.MovementReportRow{}, split.Cash.Data...), split.Electronic.Data...)
	service.SortMovementRowsDesc(legacyAll) // por fecha, mezclando efectivo y electrónico (antes: todo el efectivo primero)
	legacyData := legacyAll
	if perPage > 0 {
		start := (page - 1) * perPage
		if start >= len(legacyAll) {
			legacyData = []service.MovementReportRow{}
		} else {
			end := start + perPage
			if end > len(legacyAll) {
				end = len(legacyAll)
			}
			legacyData = legacyAll[start:end]
		}
	}
	legacyTotal := split.Cash.Total + split.Electronic.Total
	legacySummary := service.MovementChannelSummary{
		TotalRows:   legacyTotal,
		SumIncome:   split.Cash.Summary.SumIncome + split.Electronic.Summary.SumIncome,
		SumExpense:  split.Cash.Summary.SumExpense + split.Electronic.Summary.SumExpense,
		NetMovement: split.Cash.Summary.NetMovement + split.Electronic.Summary.NetMovement,
	}
	service.ApplyCurrencySummary(&legacySummary, legacyAll)
	return c.JSON(fiber.Map{
		"cash":       split.Cash,
		"electronic": split.Electronic,
		"detraction": split.Detraction,
		"data":       legacyData,
		"total":      legacyTotal,
		"summary":    legacySummary,
	})
}

// ══════════════════════════════════════════════
// BANCOS — Cuentas
// ══════════════════════════════════════════════

// GET /api/cashbank/bank-accounts
func (h *CashBankHandler) ListBankAccountsAPI(c fiber.Ctx) error {
	svc := service.NewCashBankService(db(c))
	var accounts []database.TenantBankAccount
	var err error
	if c.Query("all") == "1" {
		accounts, err = svc.ListAllBankAccounts()
	} else {
		accounts, err = svc.ListBankAccounts()
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": maskBankAccountBalances(c, accounts)})
}

// GET /api/cashbank/payment-methods — alias legacy; delega a tenant_payment_methods operativos.
func (h *CashBankHandler) ListPaymentMethodsAPI(c fiber.Ctx) error {
	svc := service.NewCashBankService(db(c))
	var (
		list interface{}
		err  error
	)
	if c.Query("all") == "1" {
		list, err = svc.ListAllPaymentMethodRecords()
	} else {
		list, err = svc.ListPaymentMethodRecords()
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}

// GET /api/cashbank/payment-methods/:id
func (h *CashBankHandler) GetPaymentMethodAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	pm, err := service.NewCashBankService(db(c)).GetPaymentMethodByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Método de pago no encontrado"})
	}
	return c.JSON(fiber.Map{"data": pm})
}

// POST /api/cashbank/payment-methods
func (h *CashBankHandler) CreatePaymentMethodAPI(c fiber.Ctx) error {
	var body struct {
		Name            string `json:"name"`
		Code            string `json:"code"`
		DestinationType   string `json:"destination_type"` // cash | bank_account
		BankAccountID     *uint  `json:"bank_account_id"`
		AppliesTo         string `json:"applies_to"`
		RequiresReference bool   `json:"requires_reference"`
		LogoURL           string `json:"logo_url"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if err := service.ValidatePaymentMethodLogo(body.LogoURL); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	svc := service.NewCashBankService(db(c))
	pm, err := svc.CreatePaymentMethod(
		body.Name, body.Code, body.DestinationType, body.BankAccountID,
	)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if body.AppliesTo != "" || body.RequiresReference {
		if err := svc.SetPaymentMethodOptions(pm.ID, body.AppliesTo, body.RequiresReference); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		pm.AppliesTo, pm.RequiresReference = body.AppliesTo, body.RequiresReference
	}
	if body.LogoURL != "" {
		if err := svc.SetPaymentMethodLogo(pm.ID, body.LogoURL); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		pm.LogoURL = body.LogoURL
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true, "data": pm})
}

// PUT /api/cashbank/payment-methods/:id
func (h *CashBankHandler) UpdatePaymentMethodAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Name            string `json:"name"`
		Code            string `json:"code"`
		DestinationType   string `json:"destination_type"`
		BankAccountID     *uint  `json:"bank_account_id"`
		Active            bool   `json:"active"`
		AppliesTo         *string `json:"applies_to"`
		RequiresReference *bool   `json:"requires_reference"`
		// LogoURL: ausente = no cambia; "" = quitar el logo propio; data URL = reemplazar.
		LogoURL *string `json:"logo_url"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.LogoURL != nil {
		if err := service.ValidatePaymentMethodLogo(*body.LogoURL); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
	}
	svc := service.NewCashBankService(db(c))
	if err := svc.UpdatePaymentMethod(
		uint(id), body.Name, body.Code, body.DestinationType, body.BankAccountID, body.Active,
	); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if body.AppliesTo != nil || body.RequiresReference != nil {
		var cur database.TenantPaymentMethod
		if err := db(c).First(&cur, id).Error; err == nil {
			applies, req := cur.AppliesTo, cur.RequiresReference
			if body.AppliesTo != nil {
				applies = *body.AppliesTo
			}
			if body.RequiresReference != nil {
				req = *body.RequiresReference
			}
			if err := svc.SetPaymentMethodOptions(uint(id), applies, req); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
			}
		}
	}
	if body.LogoURL != nil {
		if err := svc.SetPaymentMethodLogo(uint(id), *body.LogoURL); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
	}
	return c.JSON(fiber.Map{"success": true})
}

// DELETE /api/cashbank/payment-methods/:id
func (h *CashBankHandler) DeletePaymentMethodAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewCashBankService(db(c)).DeletePaymentMethod(uint(id)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/cashbank/bank-accounts/:id
func (h *CashBankHandler) GetBankAccountAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	acc, err := service.NewCashBankService(db(c)).GetBankAccountByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Cuenta no encontrada"})
	}
	return c.JSON(fiber.Map{"data": maskBankAccountBalance(c, acc)})
}

// POST /api/cashbank/bank-accounts
func (h *CashBankHandler) CreateBankAccountAPI(c fiber.Ctx) error {
	var body struct {
		Name           string  `json:"name"`
		BankName       string  `json:"bank_name"`
		AccountNumber  string  `json:"account_number"`
		CCI            string  `json:"cci"`
		Currency       string  `json:"currency"`
		Type           string  `json:"type"`
		PaymentMethod  string  `json:"payment_method"`
		InitialBalance float64 `json:"initial_balance"`
		BankID         *uint   `json:"bank_id"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.Type == "" {
		body.Type = "bank"
	}
	svc := service.NewCashBankService(db(c))
	acc, err := svc.CreateBankAccount(
		body.Name, body.BankName, body.AccountNumber, body.CCI, body.Currency, body.Type, body.PaymentMethod, body.InitialBalance,
	)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if body.BankID != nil && *body.BankID > 0 {
		if err := svc.SetBankAccountBank(acc.ID, body.BankID); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		acc.BankID = body.BankID
		if fresh, err := svc.GetBankAccountByID(acc.ID); err == nil {
			acc = fresh
		}
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true, "data": acc})
}

// PUT /api/cashbank/bank-accounts/:id
func (h *CashBankHandler) UpdateBankAccountAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Name          string `json:"name"`
		BankName      string `json:"bank_name"`
		AccountNumber string `json:"account_number"`
		CCI           string `json:"cci"`
		Type          string `json:"type"`
		PaymentMethod string `json:"payment_method"`
		Active        bool   `json:"active"`
		BankID        *uint  `json:"bank_id"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.Type == "" {
		body.Type = "bank"
	}
	svc := service.NewCashBankService(db(c))
	if err := svc.UpdateBankAccount(uint(id), body.Name, body.BankName, body.AccountNumber, body.CCI, body.Type, body.PaymentMethod, body.Active); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if body.BankID != nil {
		if err := svc.SetBankAccountBank(uint(id), body.BankID); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/cashbank/bank-accounts/:id/movements?page=&per_page=&from=&to=&type=
func (h *CashBankHandler) GetBankMovementsAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	page, _ := strconv.Atoi(c.Query("page"))
	perPage, _ := strconv.Atoi(c.Query("per_page"))
	params := service.BankMovementListParams{
		Type:    c.Query("type"),
		Page:    page,
		PerPage: perPage,
	}
	if from := c.Query("from"); from != "" {
		if t, err := time.ParseInLocation("2006-01-02", from, time.Local); err == nil {
			start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
			params.DateFrom = &start
		}
	}
	if to := c.Query("to"); to != "" {
		if t, err := time.ParseInLocation("2006-01-02", to, time.Local); err == nil {
			end := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.Local)
			params.DateTo = &end
		}
	}
	movements, total, summary, err := service.NewCashBankService(db(c)).ListBankMovementsPaged(uint(id), params)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": movements, "total": total, "summary": summary})
}

// POST /api/cashbank/bank-accounts/:id/movements
func (h *CashBankHandler) AddBankMovementAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Type        string  `json:"type"` // credit | debit
		Description string  `json:"description"`
		Reference   string  `json:"reference"`
		Amount      float64 `json:"amount"`
		Date        string  `json:"date"` // YYYY-MM-DD
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	// ParseInLocation (no Parse a secas): la conexión MySQL usa loc=Local, así que un date-only
	// parseado como UTC se corre un día hacia atrás al guardarse (medianoche UTC = 19:00 del día
	// anterior en hora Perú) — se detectó al construir el filtro from/to de ListBankMovementsPaged.
	date, err := time.ParseInLocation("2006-01-02", body.Date, time.Local)
	if err != nil {
		date = time.Now()
	}
	if err := service.NewCashBankService(db(c)).AddBankMovement(
		uint(id), userID(c), body.Type, body.Description, body.Reference, body.Amount, date,
	); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true})
}

// ReconcileBankAccountsAPI GET /cashbank/bank-accounts/reconcile — cuadre (solo lectura) del saldo de cada cuenta contra sus movimientos.
func (h *CashBankHandler) ReconcileBankAccountsAPI(c fiber.Ctx) error {
	rows, err := service.NewCashBankService(db(c)).ReconcileBankAccounts()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// CreateTransferAPI POST /cashbank/transfers — transferencia entre la caja y cuentas bancarias (o entre cuentas).
func (h *CashBankHandler) CreateTransferAPI(c fiber.Ctx) error {
	var body struct {
		service.TransferInput
		BranchID uint   `json:"branch_id"`
		Date     string `json:"date"` // YYYY-MM-DD (opcional)
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	in := body.TransferInput
	in.UserID, in.BranchID = userID(c), branchID
	if body.Date != "" {
		if d, err := time.ParseInLocation("2006-01-02", body.Date, time.Local); err == nil {
			// Mediodía: evita que un cambio de huso lo corra al día anterior.
			d = time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, d.Location())
			in.Date = &d
		}
	}
	tr, err := service.NewCashBankService(db(c)).CreateTransfer(in)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": tr})
}

// ListTransfersAPI GET /cashbank/transfers — transferencias recientes.
func (h *CashBankHandler) ListTransfersAPI(c fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	rows, err := service.NewCashBankService(db(c)).ListTransfers(limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// VoidTransferAPI POST /cashbank/transfers/:id/void — anula una transferencia (movimientos inversos).
func (h *CashBankHandler) VoidTransferAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Reason   string `json:"reason"`
		BranchID uint   `json:"branch_id"`
	}
	_ = c.Bind().JSON(&body)
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	if err := service.NewCashBankService(db(c)).VoidTransfer(uint(id), userID(c), branchID, body.Reason); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/cashbank/banks — catálogo de bancos (?all=1 incluye inactivos).
func (h *CashBankHandler) ListBanksAPI(c fiber.Ctx) error {
	rows, err := service.NewCashBankService(db(c)).ListBanks(c.Query("all") == "1")
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// POST /api/cashbank/banks
func (h *CashBankHandler) CreateBankAPI(c fiber.Ctx) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	b, err := service.NewCashBankService(db(c)).CreateBank(body.Name)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": b})
}

// PUT /api/cashbank/banks/:id
func (h *CashBankHandler) UpdateBankAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Name   string `json:"name"`
		Active bool   `json:"active"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if err := service.NewCashBankService(db(c)).UpdateBank(uint(id), body.Name, body.Active); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}
