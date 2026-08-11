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

// GET /api/cashbank/sessions?branch_id=
func (h *CashBankHandler) ListSessionsAPI(c fiber.Ctx) error {
	req, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(req))
	sessions, err := service.NewCashBankService(db(c)).ListSessionsEnriched(branchID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": filterSessionsForCaller(c, sessions)})
}

// GET /api/cashbank/sessions/open/list?branch_id= — cajas abiertas en sucursal (solo lectura).
func (h *CashBankHandler) ListOpenSessionsInBranchAPI(c fiber.Ctx) error {
	req, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ActiveBranchID(c)
	if req > 0 {
		branchID = uint(req)
	}
	if branchID == 0 {
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
	if branchID == 0 {
		branchID = branch.ActiveBranchID(c)
	}
	if branchID == 0 {
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
	if !canAccessCashSession(c, sess) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "No puede ver movimientos de esta sesión"})
	}
	movements, err := svc.GetMovements(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": movements})
}

// POST /api/cashbank/sessions/:id/movements
func (h *CashBankHandler) AddMovementAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Type           string  `json:"type"`      // income | expense
		Category       string  `json:"category"`
		Reference      string  `json:"reference"`
		PaymentMethod  string  `json:"payment_method"`
		Amount         float64 `json:"amount"`
		Notes          string  `json:"notes"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	svc := service.NewCashBankService(db(c))
	if _, err := svc.ValidateCashSessionForUser(uint(id), userID(c), branch.ActiveBranchID(c)); err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	if err := svc.AddMovement(
		uint(id), userID(c), body.Type, body.Category, body.Reference, body.PaymentMethod, body.Amount, body.Notes,
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
	if !canAccessCashSession(c, sess) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "No puede ver el reporte de esta sesión"})
	}
	rows, err := svc.GetSessionProductsReport(uint(id))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// GET /api/cashbank/reports/movements?branch_id=&user_id=&date_from=&date_to=&session_id=&type=&payment_method=&page=&per_page=
// per_page=0 u omitido: todas las filas (compatibilidad con vistas que agrupan totales).
func (h *CashBankHandler) ListMovementsReportAPI(c fiber.Ctx) error {
	var f service.MovementReportFilters
	if v, err := strconv.ParseUint(c.Query("branch_id"), 10, 32); err == nil {
		f.BranchID = branch.ResolveReadBranchFilter(c, uint(v))
	} else {
		f.BranchID = branch.ActiveBranchID(c)
	}
	if scoped := callerUserIDOrZero(c); scoped > 0 {
		f.UserID = scoped
	} else if v, err := strconv.ParseUint(c.Query("user_id"), 10, 32); err == nil {
		f.UserID = uint(v)
	}
	if v, err := strconv.ParseUint(c.Query("session_id"), 10, 32); err == nil {
		f.SessionID = uint(v)
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
		DestinationType string `json:"destination_type"` // cash | bank_account
		BankAccountID   *uint  `json:"bank_account_id"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	pm, err := service.NewCashBankService(db(c)).CreatePaymentMethod(
		body.Name, body.Code, body.DestinationType, body.BankAccountID,
	)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
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
		DestinationType string `json:"destination_type"`
		BankAccountID   *uint  `json:"bank_account_id"`
		Active          bool   `json:"active"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if err := service.NewCashBankService(db(c)).UpdatePaymentMethod(
		uint(id), body.Name, body.Code, body.DestinationType, body.BankAccountID, body.Active,
	); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
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
		Currency       string  `json:"currency"`
		Type           string  `json:"type"`
		PaymentMethod  string  `json:"payment_method"`
		InitialBalance float64 `json:"initial_balance"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.Type == "" {
		body.Type = "bank"
	}
	acc, err := service.NewCashBankService(db(c)).CreateBankAccount(
		body.Name, body.BankName, body.AccountNumber, body.Currency, body.Type, body.PaymentMethod, body.InitialBalance,
	)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
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
		Type          string `json:"type"`
		PaymentMethod string `json:"payment_method"`
		Active        bool   `json:"active"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.Type == "" {
		body.Type = "bank"
	}
	if err := service.NewCashBankService(db(c)).UpdateBankAccount(uint(id), body.Name, body.BankName, body.AccountNumber, body.Type, body.PaymentMethod, body.Active); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/cashbank/bank-accounts/:id/movements
func (h *CashBankHandler) GetBankMovementsAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	movements, err := service.NewCashBankService(db(c)).ListBankMovements(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": movements})
}

// POST /api/cashbank/bank-accounts/:id/movements
func (h *CashBankHandler) AddBankMovementAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Type        string  `json:"type"`        // credit | debit
		Description string  `json:"description"`
		Reference   string  `json:"reference"`
		Amount      float64 `json:"amount"`
		Date        string  `json:"date"` // YYYY-MM-DD
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	date, _ := time.Parse("2006-01-02", body.Date)
	if date.IsZero() {
		date = time.Now()
	}
	if err := service.NewCashBankService(db(c)).AddBankMovement(
		uint(id), userID(c), body.Type, body.Description, body.Reference, body.Amount, date,
	); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true})
}
