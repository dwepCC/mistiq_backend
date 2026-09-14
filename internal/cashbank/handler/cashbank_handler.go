package handler

import (
	"strconv"
	"time"

	"tukifac/config"
	"tukifac/internal/cashbank/service"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type CashBankHandler struct{}

func NewCashBankHandler() *CashBankHandler { return &CashBankHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}
func email(c fiber.Ctx) string {
	v, _ := c.Locals("user_email").(string)
	return v
}
func userID(c fiber.Ctx) uint {
	v, _ := c.Locals("user_id").(uint)
	return v
}
func tenantName(c fiber.Ctx) string {
	if t, ok := c.Locals("tenant").(*database.Tenant); ok && t != nil {
		return t.Name
	}
	return ""
}

// SessionView extiende TenantCashSession con campos calculados para las vistas.
type SessionView struct {
	database.TenantCashSession
	TotalIn        float64
	TotalOut       float64
	CurrentBalance float64
}

// calcSessionTotals calcula ingresos, egresos y saldo de una sesión.
// calcSessionTotals saldo de la página server-rendered "Caja" (/cashbank/cash) — mismo criterio
// que CashBankService.getExpectedBalance/sessionMovementTotals: solo cuentan los movimientos
// cuyo payment_method es efectivo (service.IsCashPaymentMethod). Antes sumaba/restaba cualquier
// movimiento de la sesión sin mirar el método, así que un egreso por transferencia (plata que
// salió del banco, no del cajón) restaba del efectivo igual que uno real.
func calcSessionTotals(tdb *gorm.DB, sessionID uint, opening float64) (totalIn, totalOut, current float64) {
	var movements []database.TenantCashMovement
	tdb.Where("cash_session_id = ? AND type IN ?", sessionID, []string{"income", "expense"}).
		Find(&movements)
	for _, m := range movements {
		if !service.IsCashPaymentMethod(m.PaymentMethod) {
			continue
		}
		if m.Type == "income" {
			totalIn += m.Amount
		} else {
			totalOut += m.Amount
		}
	}
	current = opening + totalIn - totalOut
	return
}

// getFirstBranchID obtiene el ID de la primera sucursal activa, o 0 si no hay.
func getFirstBranchID(tdb *gorm.DB) uint {
	var branch database.TenantBranch
	tdb.Where("active = ?", true).Order("id ASC").First(&branch)
	return branch.ID
}

// =================== CAJA ===================

func (h *CashBankHandler) CashIndexPage(c fiber.Ctx) error {
	tdb := db(c)
	svc := service.NewCashBankService(tdb)

	sessions, _, _ := svc.ListSessions(service.SessionListParams{})

	// Buscar sesión abierta (cualquier sucursal)
	var openRaw database.TenantCashSession
	var openSession *SessionView
	if err := tdb.Where("status = ?", "open").Order("opened_at DESC").First(&openRaw).Error; err == nil {
		in, out, cur := calcSessionTotals(tdb, openRaw.ID, openRaw.OpeningBalance)
		openSession = &SessionView{
			TenantCashSession: openRaw,
			TotalIn:           in,
			TotalOut:          out,
			CurrentBalance:    cur,
		}
	}

	// Convertir historial a SessionView con totales
	sessionViews := make([]SessionView, 0, len(sessions))
	for _, s := range sessions {
		in, out, cur := calcSessionTotals(tdb, s.ID, s.OpeningBalance)
		sessionViews = append(sessionViews, SessionView{
			TenantCashSession: s,
			TotalIn:           in,
			TotalOut:          out,
			CurrentBalance:    cur,
		})
	}

	return c.Render("cashbank/cash", fiber.Map{
		"Title":       "Caja",
		"UserEmail":   email(c),
		"TenantName":  tenantName(c),
		"IsDev":       config.AppConfig.IsDev(),
		"OpenSession": openSession,
		"Sessions":    sessionViews,
		"Success":     c.Query("success"),
	}, "layouts/base")
}

func (h *CashBankHandler) OpenSessionPage(c fiber.Ctx) error {
	tdb := db(c)
	var branches []database.TenantBranch
	tdb.Where("active = ?", true).Order("name ASC").Find(&branches)

	return c.Render("cashbank/open_session", fiber.Map{
		"Title":      "Abrir Caja",
		"UserEmail":  email(c),
		"TenantName": tenantName(c),
		"IsDev":      config.AppConfig.IsDev(),
		"Branches":   branches,
	}, "layouts/base")
}

func (h *CashBankHandler) OpenSessionForm(c fiber.Ctx) error {
	tdb := db(c)

	branchID, _ := strconv.ParseUint(c.FormValue("branch_id"), 10, 32)
	if branchID == 0 {
		// Usar primera sucursal activa si no se especificó
		branchID = uint64(getFirstBranchID(tdb))
	}
	openingBalance, _ := strconv.ParseFloat(c.FormValue("opening_balance"), 64)

	svc := service.NewCashBankService(tdb)
	_, err := svc.OpenSession(service.OpenSessionInput{
		BranchID: uint(branchID), UserID: userID(c), OpeningBalance: openingBalance, Notes: c.FormValue("notes"),
	})
	if err != nil {
		var branches []database.TenantBranch
		tdb.Where("active = ?", true).Find(&branches)
		return c.Render("cashbank/open_session", fiber.Map{
			"Title":      "Abrir Caja",
			"UserEmail":  email(c),
			"TenantName": tenantName(c),
			"IsDev":      config.AppConfig.IsDev(),
			"Branches":   branches,
			"Error":      err.Error(),
		}, "layouts/base")
	}
	return c.Redirect().To("/cashbank/cash?success=opened")
}

func (h *CashBankHandler) SessionDetailPage(c fiber.Ctx) error {
	sessionID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}

	tdb := db(c)
	var session database.TenantCashSession
	if err := tdb.First(&session, sessionID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).SendString("Sesión no encontrada")
	}

	svc := service.NewCashBankService(tdb)
	movements, _ := svc.GetMovements(uint(sessionID))

	totalIn, totalOut, current := calcSessionTotals(tdb, uint(sessionID), session.OpeningBalance)
	sv := SessionView{
		TenantCashSession: session,
		TotalIn:           totalIn,
		TotalOut:          totalOut,
		CurrentBalance:    current,
	}

	return c.Render("cashbank/session_detail", fiber.Map{
		"Title":      "Detalle de Caja",
		"UserEmail":  email(c),
		"TenantName": tenantName(c),
		"IsDev":      config.AppConfig.IsDev(),
		"Session":    sv,
		"Movements":  movements,
	}, "layouts/base")
}

func (h *CashBankHandler) CloseSessionPage(c fiber.Ctx) error {
	sessionID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}

	tdb := db(c)
	var session database.TenantCashSession
	if err := tdb.First(&session, sessionID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).SendString("Sesión no encontrada")
	}

	totalIn, totalOut, current := calcSessionTotals(tdb, uint(sessionID), session.OpeningBalance)
	sv := SessionView{
		TenantCashSession: session,
		TotalIn:           totalIn,
		TotalOut:          totalOut,
		CurrentBalance:    current,
	}

	return c.Render("cashbank/close_session", fiber.Map{
		"Title":      "Cerrar Caja",
		"UserEmail":  email(c),
		"TenantName": tenantName(c),
		"IsDev":      config.AppConfig.IsDev(),
		"Session":    sv,
	}, "layouts/base")
}

func (h *CashBankHandler) CloseSessionForm(c fiber.Ctx) error {
	sessionID, _ := strconv.ParseUint(c.Params("id"), 10, 32)
	closingBalance, _ := strconv.ParseFloat(c.FormValue("closing_balance"), 64)

	svc := service.NewCashBankService(db(c))
	if err := svc.CloseSession(uint(sessionID), userID(c), closingBalance, c.FormValue("notes"), nil, canManageAnyCashSession(c)); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}
	return c.Redirect().To("/cashbank/cash?success=closed")
}

func (h *CashBankHandler) MovementPage(c fiber.Ctx) error {
	sessionID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}

	tdb := db(c)
	var session database.TenantCashSession
	if err := tdb.First(&session, sessionID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).SendString("Sesión no encontrada")
	}

	return c.Render("cashbank/movement", fiber.Map{
		"Title":      "Nuevo Movimiento",
		"UserEmail":  email(c),
		"TenantName": tenantName(c),
		"IsDev":      config.AppConfig.IsDev(),
		"Session":    session,
	}, "layouts/base")
}

func (h *CashBankHandler) AddMovementForm(c fiber.Ctx) error {
	sessionID, _ := strconv.ParseUint(c.Params("id"), 10, 32)
	amount, _ := strconv.ParseFloat(c.FormValue("amount"), 64)
	var contactID *uint
	if v, err := strconv.ParseUint(c.FormValue("contact_id"), 10, 32); err == nil && v > 0 {
		cid := uint(v)
		contactID = &cid
	}

	svc := service.NewCashBankService(db(c))
	if err := svc.AddMovement(
		uint(sessionID),
		userID(c),
		c.FormValue("type"),
		c.FormValue("category"),
		c.FormValue("reference"),
		c.FormValue("payment_method"),
		amount,
		c.FormValue("notes"),
		contactID,
	); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}
	return c.Redirect().To("/cashbank/cash/" + c.Params("id"))
}

// =================== BANCOS ===================

func (h *CashBankHandler) BankIndexPage(c fiber.Ctx) error {
	svc := service.NewCashBankService(db(c))
	accounts, _ := svc.ListBankAccounts()
	return c.Render("cashbank/bank", fiber.Map{
		"Title":      "Cuentas Bancarias",
		"UserEmail":  email(c),
		"TenantName": tenantName(c),
		"IsDev":      config.AppConfig.IsDev(),
		"Accounts":   accounts,
		"Success":    c.Query("success"),
	}, "layouts/base")
}

func (h *CashBankHandler) CreateBankAccountForm(c fiber.Ctx) error {
	initialBalance, _ := strconv.ParseFloat(c.FormValue("balance"), 64)
	currency := c.FormValue("currency")
	if currency == "" {
		currency = "PEN"
	}
	accType := c.FormValue("type")
	if accType == "" {
		accType = "bank"
	}
	svc := service.NewCashBankService(db(c))
	_, err := svc.CreateBankAccount(
		c.FormValue("name"),
		c.FormValue("bank_name"),
		c.FormValue("account_number"),
		currency,
		accType,
		c.FormValue("payment_method"),
		initialBalance,
	)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}
	return c.Redirect().To("/cashbank/bank?success=created")
}

func (h *CashBankHandler) BankMovementsPage(c fiber.Ctx) error {
	accountID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}

	tdb := db(c)
	var account database.TenantBankAccount
	if err := tdb.First(&account, accountID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).SendString("Cuenta no encontrada")
	}

	svc := service.NewCashBankService(tdb)
	movements, _ := svc.ListBankMovements(uint(accountID))

	return c.Render("cashbank/bank_movements", fiber.Map{
		"Title":      "Movimientos — " + account.Name,
		"UserEmail":  email(c),
		"TenantName": tenantName(c),
		"IsDev":      config.AppConfig.IsDev(),
		"Account":    account,
		"Movements":  movements,
		"Success":    c.Query("success"),
	}, "layouts/base")
}

func (h *CashBankHandler) AddBankMovementForm(c fiber.Ctx) error {
	accountID, _ := strconv.ParseUint(c.Params("id"), 10, 32)
	amount, _ := strconv.ParseFloat(c.FormValue("amount"), 64)
	date := time.Now()
	if d := c.FormValue("date"); d != "" {
		if t, err := time.Parse("2006-01-02", d); err == nil {
			date = t
		}
	}

	svc := service.NewCashBankService(db(c))
	if err := svc.AddBankMovement(
		uint(accountID),
		userID(c),
		c.FormValue("type"),
		c.FormValue("description"),
		c.FormValue("reference"),
		amount, date,
	); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}
	return c.Redirect().To("/cashbank/bank/" + c.Params("id") + "?success=added")
}
