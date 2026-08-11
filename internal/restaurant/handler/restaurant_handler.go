package handler

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"tukifac/config"
	"tukifac/internal/restaurant/service"
	"tukifac/internal/restaurant/staff"
	billingsvc "tukifac/internal/billing/service"
	salesvc "tukifac/internal/sales/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"
	"tukifac/pkg/restaurantperm"
	"tukifac/pkg/saas"
	"tukifac/pkg/saas/docusage"
	"tukifac/pkg/tax"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type RestaurantHandler struct{}

func New() *RestaurantHandler { return &RestaurantHandler{} }

func db(c fiber.Ctx) *gorm.DB  { v, _ := c.Locals("tenantDB").(*gorm.DB); return v }
func uid(c fiber.Ctx) uint      { v, _ := c.Locals("user_id").(uint); return v }
func activeBranch(c fiber.Ctx) (uint, error) {
	id := branch.ActiveBranchID(c)
	if id == 0 {
		return 0, errors.New("sucursal activa requerida")
	}
	return id, nil
}
func svc(c fiber.Ctx) *service.RestaurantService { return service.New(db(c)) }

// buildRestaurantPrintData arma print_data reutilizando el `sale` YA en memoria
// (evita el re-fetch completo de BuildPrintDataForSale: venta + billing status + pagos + invoice).
// Solo consulta los ítems una vez, igual que el flujo de ventas de TukiFac. El sunatHash va
// vacío porque a la hora de imprimir el CDR aún no existe (envío fiscal asíncrono), idéntico
// a lo que devolvía BuildPrintDataForSale en este punto.
func buildRestaurantPrintData(gdb *gorm.DB, sale *database.TenantSale, payments []service.PaymentInput) *salesvc.PrintData {
	items, _ := salesvc.NewSaleService(gdb).GetItems(sale.ID)
	printPayments := make([]salesvc.PrintPaymentInput, 0, len(payments))
	for _, p := range payments {
		printPayments = append(printPayments, salesvc.PrintPaymentInput{
			Method:    p.Method,
			Amount:    p.Amount,
			Reference: p.Reference,
		})
	}
	pd, _ := salesvc.BuildPrintData(gdb, sale, items, printPayments, "")
	return pd
}

func resolveSessionStaffID(c fiber.Ctx, requested *uint) *uint {
	staffSvc := staff.New(db(c))
	claims, _ := c.Locals("tenant_claims").(*middleware.TenantClaims)
	var staffID *uint
	if claims != nil && claims.StaffID > 0 {
		sid := claims.StaffID
		staffID = &sid
	} else if st, err := staffSvc.GetStaffByUserID(uid(c)); err == nil {
		sid := st.ID
		staffID = &sid
	}
	if requested != nil && *requested > 0 && middleware.HasRestaurantPerm(c, restaurantperm.SettingsManage) {
		staffID = requested
	}
	return staffID
}

// GET /api/restaurant/settings
func (h *RestaurantHandler) GetSettings(c fiber.Ctx) error {
	hasPin, err := svc(c).HasDeletionPin()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"has_deletion_pin": hasPin})
}

// PUT /api/restaurant/settings  — guarda el PIN de anulación (desde panel tenant)
func (h *RestaurantHandler) UpdateSettings(c fiber.Ctx) error {
	var body struct {
		DeletionPin string `json:"deletion_pin"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if err := svc(c).SaveRestaurantSettings(body.DeletionPin); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// ================================================================
// PISOS
// ================================================================

// GET /api/restaurant/floors
func (h *RestaurantHandler) ListFloors(c fiber.Ctx) error {
	bid, err := activeBranch(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchRequired})
	}
	floors, err := svc(c).ListFloors(bid)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": floors})
}

// POST /api/restaurant/floors
func (h *RestaurantHandler) CreateFloor(c fiber.Ctx) error {
	var body struct {
		Name  string `json:"name"`
		Order int    `json:"sort_order"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	bid, err := activeBranch(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchRequired})
	}
	f, err := svc(c).CreateFloor(bid, body.Name, body.Order)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"success": true, "data": f})
}

// PUT /api/restaurant/floors/:id
func (h *RestaurantHandler) UpdateFloor(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		Name   string `json:"name"`
		Order  int    `json:"sort_order"`
		Active bool   `json:"active"`
	}
	body.Active = true
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if err := svc(c).UpdateFloor(id, body.Name, body.Order, body.Active); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// DELETE /api/restaurant/floors/:id
func (h *RestaurantHandler) DeleteFloor(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	if err := svc(c).DeleteFloor(id); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// ================================================================
// MESAS
// ================================================================

// GET /api/restaurant/tables?floor_id=
func (h *RestaurantHandler) ListTables(c fiber.Ctx) error {
	floorID, _ := strconv.ParseUint(c.Query("floor_id"), 10, 32)
	bid, err := activeBranch(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchRequired})
	}
	tables, err := svc(c).ListTables(bid, uint(floorID))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": tables})
}

// POST /api/restaurant/tables
func (h *RestaurantHandler) CreateTable(c fiber.Ctx) error {
	var body struct {
		FloorID  uint   `json:"floor_id"`
		Name     string `json:"name"`
		Capacity int    `json:"capacity"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if body.Capacity == 0 {
		body.Capacity = 4
	}
	bid, err := activeBranch(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchRequired})
	}
	t, err := svc(c).CreateTable(bid, body.FloorID, body.Name, body.Capacity)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"success": true, "data": t})
}

// PUT /api/restaurant/tables/:id
func (h *RestaurantHandler) UpdateTable(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		FloorID  *uint  `json:"floor_id"`
		Name     string `json:"name"`
		Capacity int    `json:"capacity"`
		Active   *bool  `json:"active"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	active := true
	if body.Active != nil {
		active = *body.Active
	}
	if err := svc(c).UpdateTable(id, body.FloorID, body.Name, body.Capacity, active); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// DELETE /api/restaurant/tables/:id
func (h *RestaurantHandler) DeleteTable(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	if err := svc(c).DeleteTable(id); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// ================================================================
// SESIONES (APERTURA / CIERRE DE MESA)
// ================================================================

// POST /api/restaurant/sessions  — abre mesa o pedido (llevar, delivery, POS)
func (h *RestaurantHandler) OpenSession(c fiber.Ctx) error {
	var body struct {
		TableID           *uint  `json:"table_id"`
		StaffID           *uint  `json:"staff_id"`
		Guests            int    `json:"guests"`
		Notes             string `json:"notes"`
		OrderType         string `json:"order_type"`
		ContactID         *uint  `json:"contact_id"`
		CustomerName      string `json:"customer_name"`
		CustomerPhone     string `json:"customer_phone"`
		DeliveryDriverID  *uint  `json:"delivery_driver_id"`
		DeliveryAddress   string `json:"delivery_address"`
		DeliveryReference string `json:"delivery_reference"`
		EstimatedMinutes  int    `json:"estimated_minutes"`
		SaveAsDraft       bool   `json:"save_as_draft"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if body.Guests == 0 {
		body.Guests = 1
	}
	bid, err := activeBranch(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchRequired})
	}
	staffID := resolveSessionStaffID(c, body.StaffID)
	sess, err := svc(c).OpenTableExtended(service.OpenSessionInput{
		TableID: body.TableID, StaffID: staffID, BranchID: bid, UserID: uid(c),
		Guests: body.Guests, Notes: body.Notes, OrderType: body.OrderType,
		ContactID: body.ContactID, CustomerName: body.CustomerName, CustomerPhone: body.CustomerPhone,
		DeliveryDriverID: body.DeliveryDriverID, DeliveryAddress: body.DeliveryAddress,
		DeliveryReference: body.DeliveryReference, EstimatedMinutes: body.EstimatedMinutes,
		SaveAsDraft: body.SaveAsDraft,
	})
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"success": true, "data": sess})
}

// GET /api/restaurant/sessions/:id
func (h *RestaurantHandler) GetSession(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	detail, err := svc(c).GetSessionDetail(id)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": detail})
}

// GET /api/restaurant/tables/:id/session
func (h *RestaurantHandler) GetTableSession(c fiber.Ctx) error {
	tableID, err := parseID(c)
	if err != nil {
		return err
	}
	sess, err := svc(c).GetActiveSessionByTable(tableID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if sess == nil {
		return c.Status(404).JSON(fiber.Map{"error": "no hay sesión activa en esta mesa"})
	}
	detail, _ := svc(c).GetSessionDetail(sess.ID)
	return c.JSON(fiber.Map{"data": detail})
}

// POST /api/restaurant/sessions/:id/cancel
func (h *RestaurantHandler) CancelSession(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		Reason string `json:"reason"`
		Pin    string `json:"pin"`
	}
	if err := c.Bind().JSON(&body); err != nil || body.Reason == "" || strings.TrimSpace(body.Pin) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "se requiere motivo de anulación y PIN"})
	}
	if err := svc(c).CancelSession(id, body.Pin, body.Reason, uid(c)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/restaurant/sessions/:id/cancel-comandas
func (h *RestaurantHandler) CancelAllComandas(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		Reason  string `json:"reason"`
		Pin     string `json:"pin"`
		OrderID *uint  `json:"order_id"`
	}
	if err := c.Bind().JSON(&body); err != nil || body.Reason == "" || strings.TrimSpace(body.Pin) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "se requiere motivo de anulación y PIN"})
	}
	res, err := svc(c).CancelAllComandas(id, body.OrderID, body.Pin, body.Reason, uid(c))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": res})
}

// ================================================================
// PEDIDOS
// ================================================================

// POST /api/restaurant/sessions/:id/orders
func (h *RestaurantHandler) AddOrder(c fiber.Ctx) error {
	sessionID, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		StaffID *uint                  `json:"staff_id"`
		Notes   string                 `json:"notes"`
		Items   []service.NewOrderItem `json:"items"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	staffID := resolveSessionStaffID(c, body.StaffID)
	order, err := svc(c).AddOrder(sessionID, staffID, uid(c), body.Items, body.Notes)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"success": true, "data": order})
}

// ================================================================
// COMANDAS
// ================================================================

// PATCH /api/restaurant/comandas/:id/notes
func (h *RestaurantHandler) UpdateComandaNotes(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		Notes string `json:"notes"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if err := svc(c).UpdateComandaNotes(id, body.Notes); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// PUT /api/restaurant/comandas/:id/status
func (h *RestaurantHandler) UpdateComandaStatus(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if err := svc(c).UpdateComandaStatus(id, body.Status, uid(c)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// DELETE /api/restaurant/comandas/:id   (anulación; requiere PIN de ajustes)
func (h *RestaurantHandler) CancelComanda(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		Reason string `json:"reason"`
		Pin    string `json:"pin"`
	}
	if err := c.Bind().JSON(&body); err != nil || body.Reason == "" || strings.TrimSpace(body.Pin) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "se requiere motivo de anulación y PIN"})
	}
	if err := svc(c).VerifyDeletionPin(body.Pin); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if err := svc(c).CancelComanda(id, body.Reason, uid(c)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/restaurant/comandas/:id/print
func (h *RestaurantHandler) PrintComanda(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	if err := svc(c).MarkComandaPrinted(id, uid(c)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/restaurant/table-orders/:id/printed — confirma impresión de toda la ronda (ticket)
func (h *RestaurantHandler) MarkTableOrderPrinted(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	if err := svc(c).MarkTableOrderPrinted(id, uid(c)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/restaurant/kitchen   — vista de cocina: comandas activas
func (h *RestaurantHandler) KitchenView(c fiber.Ctx) error {
	bid, err := activeBranch(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchRequired})
	}
	comandas, err := svc(c).GetKitchenComandas(bid)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": comandas})
}

// ================================================================
// COBRO DE MESA
// ================================================================

// POST /api/restaurant/sessions/:id/bill
func (h *RestaurantHandler) BillSession(c fiber.Ctx) error {
	sessionID, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		SeriesID       uint                   `json:"series_id"`
		DocType        string                 `json:"doc_type"`
		Currency       string                 `json:"currency"`
		ContactID      *uint                  `json:"contact_id"`
		CashSessionID  *uint                  `json:"cash_session_id"`
		IssueDate      string                 `json:"issue_date"`
		CloseSession   *bool                  `json:"close_session"` // true = cerrar mesa tras cobrar; false = solo generar venta, mesa sigue abierta
		Payments       []service.PaymentInput `json:"payments"`
		DiscountAmount float64                `json:"discount_amount"`
		DiscountMode   string                 `json:"discount_mode"`
		DiscountValue  float64                `json:"discount_value"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if len(body.Payments) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "se requiere al menos un método de pago"})
	}
	if body.SeriesID == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "se requiere una serie de documento"})
	}
	closeSession := true
	if body.CloseSession != nil {
		closeSession = *body.CloseSession
	}

	issueDate := saas.NowLima()
	if body.IssueDate != "" {
		if t, parseErr := time.ParseInLocation("2006-01-02", body.IssueDate, saas.LimaLocation()); parseErr == nil {
			issueDate = time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, saas.LimaLocation())
		}
	}

	dbc := db(c)
	taxCfg := tax.LoadFromDB(dbc)
	et := ""
	if claims, ok := c.Locals("tenant_claims").(*middleware.TenantClaims); ok && claims != nil {
		et = claims.EmployeeType
	}
	var centralTenantID uint
	if tenant, ok := c.Locals("tenant").(*database.Tenant); ok && tenant != nil {
		centralTenantID = tenant.ID
	}
	sale, err := service.New(dbc).BillTable(service.BillInput{
		SessionID:       sessionID,
		UserID:          uid(c),
		EmployeeType:    et,
		SeriesID:        body.SeriesID,
		DocType:         body.DocType,
		IssueDate:       issueDate,
		Currency:        body.Currency,
		ContactID:       body.ContactID,
		Payments:        body.Payments,
		CashSessionID:   body.CashSessionID,
		CloseSession:    closeSession,
		DiscountAmount:  body.DiscountAmount,
		DiscountMode:    body.DiscountMode,
		DiscountValue:   body.DiscountValue,
		CentralTenantID: centralTenantID,
	}, taxCfg)
	if err != nil {
		st := fiber.StatusBadRequest
		payload := fiber.Map{"error": err.Error()}
		if errors.Is(err, docusage.ErrQuotaExceeded) {
			st = fiber.StatusPaymentRequired
			payload["code"] = "DOCUMENT_QUOTA_EXCEEDED"
		}
		return c.Status(st).JSON(payload)
	}
	if tenant, ok := c.Locals("tenant").(*database.Tenant); ok && tenant != nil {
		_ = billingsvc.TriggerAutoEnqueueAfterSaleCommit(dbc, tenant, sale.ID)
	}
	printData := buildRestaurantPrintData(dbc, sale, body.Payments)
	return c.Status(201).JSON(fiber.Map{"success": true, "data": sale, "print_data": printData})
}

// POST /api/restaurant/pos/checkout
// Checkout compuesto EXCLUSIVO del POS de venta rápida: encapsula en el servidor
// OpenSession → AddOrder → BillTable (vía RestaurantPOSCheckoutService) y devuelve la
// venta + print_data en una sola respuesta, eliminando 3 round-trips HTTP. No modifica
// el flujo de mesas/comandas/cocina. Protegido por el feature flag POS_FAST_CHECKOUT_ENABLED.
func (h *RestaurantHandler) POSCheckout(c fiber.Ctx) error {
	if config.AppConfig == nil || !config.AppConfig.POSFastCheckoutEnabled {
		return c.Status(403).JSON(fiber.Map{"error": "POS fast checkout deshabilitado", "code": "POS_FAST_CHECKOUT_DISABLED"})
	}
	var body struct {
		SessionID      *uint                  `json:"session_id"`
		OrderType      string                 `json:"order_type"`
		Guests         int                    `json:"guests"`
		Notes          string                 `json:"notes"`
		ContactID         *uint                  `json:"contact_id"`
		CustomerName      string                 `json:"customer_name"`
		CustomerPhone     string                 `json:"customer_phone"`
		DeliveryDriverID  *uint                  `json:"delivery_driver_id"`
		DeliveryAddress   string                 `json:"delivery_address"`
		DeliveryReference string                 `json:"delivery_reference"`
		EstimatedMinutes  int                    `json:"estimated_minutes"`
		StaffID           *uint                  `json:"staff_id"`
		Items             []service.NewOrderItem `json:"items"`
		SeriesID       uint                   `json:"series_id"`
		DocType        string                 `json:"doc_type"`
		Currency       string                 `json:"currency"`
		IssueDate      string                 `json:"issue_date"`
		CashSessionID  *uint                  `json:"cash_session_id"`
		DiscountMode   string                 `json:"discount_mode"`
		DiscountValue  float64                `json:"discount_value"`
		DiscountAmount float64                `json:"discount_amount"`
		Payments       []service.PaymentInput `json:"payments"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if len(body.Payments) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "se requiere al menos un método de pago"})
	}
	if body.SeriesID == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "se requiere una serie de documento"})
	}

	dbc := db(c)

	bid, err := activeBranch(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchRequired})
	}

	issueDate := saas.NowLima()
	if body.IssueDate != "" {
		if t, parseErr := time.ParseInLocation("2006-01-02", body.IssueDate, saas.LimaLocation()); parseErr == nil {
			issueDate = time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, saas.LimaLocation())
		}
	}

	taxCfg := tax.LoadFromDB(dbc)
	et := ""
	if claims, ok := c.Locals("tenant_claims").(*middleware.TenantClaims); ok && claims != nil {
		et = claims.EmployeeType
	}
	var centralTenantID uint
	if tenant, ok := c.Locals("tenant").(*database.Tenant); ok && tenant != nil {
		centralTenantID = tenant.ID
	}
	staffID := resolveSessionStaffID(c, body.StaffID)

	sale, err := service.NewRestaurantPOSCheckoutService(dbc).Checkout(service.POSCheckoutInput{
		SessionID:       body.SessionID,
		BranchID:        bid,
		UserID:          uid(c),
		EmployeeType:    et,
		StaffID:         staffID,
		OrderType:       body.OrderType,
		Guests:          body.Guests,
		Notes:           body.Notes,
		ContactID:         body.ContactID,
		CustomerName:      body.CustomerName,
		CustomerPhone:     body.CustomerPhone,
		DeliveryDriverID:  body.DeliveryDriverID,
		DeliveryAddress:   body.DeliveryAddress,
		DeliveryReference: body.DeliveryReference,
		EstimatedMinutes:  body.EstimatedMinutes,
		Items:             body.Items,
		SeriesID:        body.SeriesID,
		DocType:         body.DocType,
		Currency:        body.Currency,
		IssueDate:       issueDate,
		CashSessionID:   body.CashSessionID,
		DiscountMode:    body.DiscountMode,
		DiscountValue:   body.DiscountValue,
		DiscountAmount:  body.DiscountAmount,
		Payments:        body.Payments,
		CentralTenantID: centralTenantID,
	}, taxCfg)
	if err != nil {
		st := fiber.StatusBadRequest
		payload := fiber.Map{"error": err.Error()}
		if errors.Is(err, docusage.ErrQuotaExceeded) {
			st = fiber.StatusPaymentRequired
			payload["code"] = "DOCUMENT_QUOTA_EXCEEDED"
		}
		return c.Status(st).JSON(payload)
	}
	if tenant, ok := c.Locals("tenant").(*database.Tenant); ok && tenant != nil {
		_ = billingsvc.TriggerAutoEnqueueAfterSaleCommit(dbc, tenant, sale.ID)
	}
	printData := buildRestaurantPrintData(dbc, sale, body.Payments)
	return c.Status(201).JSON(fiber.Map{"success": true, "data": sale, "print_data": printData})
}

// POST /api/restaurant/sessions/:id/close — cierra la mesa sin generar venta (mesa ya pagada).
func (h *RestaurantHandler) CloseSession(c fiber.Ctx) error {
	sessionID, err := parseID(c)
	if err != nil {
		return err
	}
	if err := svc(c).CloseSessionOnly(sessionID); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// ================================================================
// PAGOS MÚLTIPLES (ventas generales)
// ================================================================

// POST /api/sales/:id/payments
func (h *RestaurantHandler) RegisterPayments(c fiber.Ctx) error {
	saleID, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		Payments []service.PaymentInput `json:"payments"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if len(body.Payments) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "se requiere al menos un pago"})
	}
	if err := svc(c).RegisterPayments(saleID, body.Payments, uid(c)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	printData, _ := salesvc.BuildPrintDataForSale(db(c), saleID)
	return c.Status(201).JSON(fiber.Map{"success": true, "print_data": printData})
}

// GET /api/sales/:id/payments
func (h *RestaurantHandler) GetSalePayments(c fiber.Ctx) error {
	saleID, err := parseID(c)
	if err != nil {
		return err
	}
	payments, err := svc(c).GetSalePayments(saleID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": payments})
}

// GET /api/restaurant/orders — pedidos abiertos (POS / comandas)
func (h *RestaurantHandler) ListOpenOrders(c fiber.Ctx) error {
	bid, err := activeBranch(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchRequired})
	}
	orderType := c.Query("order_type", "all")
	list, err := svc(c).ListOpenOrders(bid, orderType)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}

// PATCH /api/restaurant/sessions/:id
func (h *RestaurantHandler) UpdateSession(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body service.UpdateSessionInput
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if err := svc(c).UpdateSession(id, body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// PUT /api/restaurant/sessions/:id/order-status
func (h *RestaurantHandler) UpdateOrderStatus(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		OrderStatus string `json:"order_status"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if err := svc(c).UpdateOrderStatus(id, body.OrderStatus); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/restaurant/sessions/:id/precuenta
func (h *RestaurantHandler) GetPrecuenta(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	data, err := svc(c).GetPrecuenta(id)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": data})
}

// GET /api/restaurant/delivery-companies
func (h *RestaurantHandler) ListDeliveryCompanies(c fiber.Ctx) error {
	activeOnly := c.Query("active_only", "true") == "true"
	list, err := svc(c).ListDeliveryCompanies(activeOnly)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}

func (h *RestaurantHandler) CreateDeliveryCompany(c fiber.Ctx) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	company, err := svc(c).CreateDeliveryCompany(body.Name)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"success": true, "data": company})
}

func (h *RestaurantHandler) UpdateDeliveryCompany(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		Name      string `json:"name"`
		Active    bool   `json:"active"`
		SortOrder int    `json:"sort_order"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if err := svc(c).UpdateDeliveryCompany(id, body.Name, body.Active, body.SortOrder); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *RestaurantHandler) DeleteDeliveryCompany(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	if err := svc(c).DeleteDeliveryCompany(id); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/restaurant/delivery-drivers
func (h *RestaurantHandler) ListDeliveryDrivers(c fiber.Ctx) error {
	activeOnly := c.Query("active_only", "true") == "true"
	list, err := svc(c).ListDeliveryDrivers(activeOnly)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}

func (h *RestaurantHandler) CreateDeliveryDriver(c fiber.Ctx) error {
	var body struct {
		Name              string `json:"name"`
		Phone             string `json:"phone"`
		VehicleType       string `json:"vehicle_type"`
		Plate             string `json:"plate"`
		Notes             string `json:"notes"`
		DeliveryCompanyID *uint  `json:"delivery_company_id"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	d, err := svc(c).CreateDeliveryDriver(body.Name, body.Phone, body.VehicleType, body.Plate, body.Notes, body.DeliveryCompanyID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"success": true, "data": d})
}

func (h *RestaurantHandler) UpdateDeliveryDriver(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		Name              string `json:"name"`
		Phone             string `json:"phone"`
		VehicleType       string `json:"vehicle_type"`
		Plate             string `json:"plate"`
		Notes             string `json:"notes"`
		Active            bool   `json:"active"`
		DeliveryCompanyID *uint  `json:"delivery_company_id"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if err := svc(c).UpdateDeliveryDriver(id, body.Name, body.Phone, body.VehicleType, body.Plate, body.Notes, body.Active, body.DeliveryCompanyID); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *RestaurantHandler) DeleteDeliveryDriver(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	if err := svc(c).DeleteDeliveryDriver(id); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/restaurant/dashboard?start_date=YYYY-MM-DD&end_date=YYYY-MM-DD&top_n=10
func (h *RestaurantHandler) Dashboard(c fiber.Ctx) error {
	tdb := db(c)
	if tdb == nil {
		return c.Status(400).JSON(fiber.Map{"error": "sin contexto de empresa"})
	}
	branchID, err := activeBranch(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	from, toExclusive, err := parseDashboardDateRange(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	topN, _ := strconv.Atoi(c.Query("top_n"))
	if topN <= 0 {
		topN = 10
	}
	filter := resolveRestaurantDashboardFilter(c)
	data, err := service.NewDashboardService(tdb).GetDashboard(branchID, from, toExclusive, topN, filter)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": data})
}

func resolveRestaurantDashboardFilter(c fiber.Ctx) service.DashboardFilter {
	if middleware.HasRestaurantPerm(c, restaurantperm.SettingsManage) {
		return service.DashboardFilter{}
	}
	if claims, ok := c.Locals("tenant_claims").(*middleware.TenantClaims); ok && claims != nil {
		if claims.RoleName == "Administrador" {
			return service.DashboardFilter{}
		}
		switch strings.ToLower(strings.TrimSpace(claims.EmployeeType)) {
		case "admin", "supervisor":
			return service.DashboardFilter{}
		}
	}
	if userRole, _ := c.Locals("user_role").(string); userRole == "Administrador" {
		return service.DashboardFilter{}
	}
	userID := uid(c)
	if userID == 0 {
		return service.DashboardFilter{}
	}
	return service.DashboardFilter{UserID: userID, RestrictUser: true}
}

// ================================================================
// HELPERS
// ================================================================

func parseDashboardDateRange(c fiber.Ctx) (from, toExclusive time.Time, err error) {
	start := strings.TrimSpace(c.Query("start_date"))
	end := strings.TrimSpace(c.Query("end_date"))
	now := time.Now()
	if start == "" && end == "" {
		from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		toExclusive = from.AddDate(0, 0, 1)
		return from, toExclusive, nil
	}
	if start == "" || end == "" {
		return time.Time{}, time.Time{}, errors.New("start_date y end_date son requeridos (YYYY-MM-DD)")
	}
	f, e1 := time.ParseInLocation("2006-01-02", start, time.Local)
	t, e2 := time.ParseInLocation("2006-01-02", end, time.Local)
	if e1 != nil || e2 != nil {
		return time.Time{}, time.Time{}, errors.New("fechas inválidas (use YYYY-MM-DD)")
	}
	from = time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, time.Local)
	endDay := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	toExclusive = endDay.AddDate(0, 0, 1)
	if !toExclusive.After(from) {
		return time.Time{}, time.Time{}, errors.New("end_date debe ser >= start_date")
	}
	return from, toExclusive, nil
}

func parseID(c fiber.Ctx) (uint, error) {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		_ = c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
		return 0, err
	}
	return uint(id), nil
}
