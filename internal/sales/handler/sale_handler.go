package handler

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	billingSvc "tukifac/internal/billing/service"
	detraccionsvc "tukifac/internal/detraccion"
	salecontext "tukifac/internal/fiscal/salecontext"
	prepaymentsvc "tukifac/internal/prepayment"
	quotationsvc "tukifac/internal/quotations/service"
	"tukifac/internal/exportacion"
	"tukifac/internal/sales/branchguard"
	"tukifac/internal/sales/nvdisplay"
	"tukifac/internal/sales/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	emailpkg "tukifac/pkg/email"
	"tukifac/pkg/middleware"
	"tukifac/pkg/reportgate"
	"tukifac/pkg/saas/docusage"
	"tukifac/pkg/tax"
	"tukifac/pkg/tenantctx"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// saleListGate limita los listados/reportes de ventas simultáneos por tenant. Con el pool de 3
// conexiones de producción, 2 deja siempre una libre para login y el middleware de sesión.
var (
	saleListGate     = reportgate.New(2)
	saleListGateWait = 8 * time.Second
)

type SaleHandler struct{}

func NewSaleHandler() *SaleHandler { return &SaleHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

// saleNotFound 404 uniforme de las guardas de sucursal: no revela si el ID existe en otra sucursal.
func saleNotFound(c fiber.Ctx) error {
	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No encontrado"})
}

func email(c fiber.Ctx) string {
	v, _ := c.Locals("user_email").(string)
	return v
}
func userID(c fiber.Ctx) uint {
	v, _ := c.Locals("user_id").(uint)
	return v
}

// userCanOverridePrice resuelve sales.override_price contra el JWT del usuario autenticado
// (nunca contra el body de la request) — ver CreateSaleInput.UserCanOverridePrice.
func userCanOverridePrice(c fiber.Ctx) bool {
	claims, _ := c.Locals("tenant_claims").(*middleware.TenantClaims)
	return middleware.HasPermission(claims, "sales.override_price")
}

func (h *SaleHandler) ListPage(c fiber.Ctx) error {
	svc := service.NewSaleService(db(c))
	branchID, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)

	params := service.SaleListParams{
		BranchID: uint(branchID),
		DocType:  c.Query("doc_type"),
		Status:   c.Query("status"),
		Query:    c.Query("q"),
	}

	sales, _, _, _ := svc.List(params)
	var branches []database.TenantBranch
	db(c).Where("active = ?", true).Find(&branches)

	return c.Render("sales/index", fiber.Map{
		"Title":     "Ventas",
		"UserEmail": email(c),
		"Sales":     sales,
		"Branches":  branches,
		"Params":    params,
		"Success":   c.Query("success"),
	}, "layouts/base")
}

func (h *SaleHandler) POSPage(c fiber.Ctx) error {
	tdb := db(c)

	var branches []database.TenantBranch
	tdb.Where("active = ?", true).Find(&branches)

	// En el POS solo se muestran series de tipo "venta" (Factura, Boleta, Nota de Venta)
	var allSeries []database.TenantDocumentSeries
	tdb.Where("active = ? AND (category = 'venta' OR category = '' OR category IS NULL)", true).
		Order("branch_id, doc_type").Find(&allSeries)

	var contacts []database.TenantContact
	tdb.Where("active = ? AND (type = 'customer' OR type = 'both')", true).
		Order("business_name").Find(&contacts)

	var products []database.TenantProduct
	tdb.Where("active = ?", true).Order("name").Find(&products)

	var categories []database.TenantCategory
	tdb.Where("active = ?", true).Order("name").Find(&categories)

	var cashSession *database.TenantCashSession
	var cs database.TenantCashSession
	if err := tdb.Where("user_id = ? AND status = ?", userID(c), "open").First(&cs).Error; err == nil {
		cashSession = &cs
	}

	taxCfg := tax.LoadFromDB(tdb)

	return c.Render("sales/pos", fiber.Map{
		"Title":       "Punto de Venta",
		"UserEmail":   email(c),
		"Branches":    branches,
		"Series":      allSeries,
		"Contacts":    contacts,
		"Products":    products,
		"Categories":  categories,
		"CashSession": cashSession,
		"TaxRate":     taxCfg.TaxRate,
	}, "layouts/base")
}

func (h *SaleHandler) CreateAPI(c fiber.Ctx) error {
	var body struct {
		BranchID             uint                             `json:"branch_id"`
		ContactID            *uint                            `json:"contact_id"`
		CashSessionID        *uint                            `json:"cash_session_id"`
		SeriesID             uint                             `json:"series_id"`
		DocType              string                           `json:"doc_type"`
		IssueDate            string                           `json:"issue_date"`
		DueDate              string                           `json:"due_date"`
		Currency             string                           `json:"currency"`
		OperationTypeCode    string                           `json:"operation_type_code"`
		ExchangeRate         *float64                         `json:"exchange_rate"`
		PaymentMethod        string                           `json:"payment_method"`
		Payments             []service.PaymentInput           `json:"payments"`
		PaymentConditionCode string                           `json:"payment_condition_code"`
		CreditInstallments   []service.CreditInstallmentInput `json:"credit_installments"`
		Notes                string                           `json:"notes"`
		Items                []service.SaleItemInput          `json:"items"`
		GlobalDiscountMode   string                           `json:"global_discount_mode"`
		ApplyServiceCharge   bool                             `json:"apply_service_charge"`
		GlobalDiscountValue  float64                          `json:"global_discount_value"`
		FiscalContext        *salecontext.FiscalContextInput  `json:"fiscal_context"`
		Detraccion           *detraccionsvc.SaleInput         `json:"detraccion"`
		Export               *exportacion.SaleInput           `json:"export"`
		Prepayment           *prepaymentsvc.SaleInput         `json:"prepayment"`
		FromQuotationID      *uint                            `json:"from_quotation_id"`
		// IdempotencyKey: UUID por intento de cobro; el reintento con la misma clave devuelve la venta ya creada.
		IdempotencyKey string `json:"idempotency_key"`
	}

	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}

	// Importante: time.Parse("2006-01-02", ...) devuelve 00:00 en UTC.
	// En Perú (UTC-5) eso se ve como el día anterior, por eso guardamos fecha en America/Lima.
	// En algunos contenedores Linux no está instalado tzdata, así que si falla usamos time.Local.
	loc, err := time.LoadLocation("America/Lima")
	if err != nil || loc == nil {
		loc = time.Local
	}
	nowPe := time.Now().In(loc)
	issueDate := time.Date(nowPe.Year(), nowPe.Month(), nowPe.Day(), 12, 0, 0, 0, loc) // mediodía evita cruces de día
	if body.IssueDate != "" {
		if t, err := time.ParseInLocation("2006-01-02", body.IssueDate, loc); err == nil {
			issueDate = time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
		}
	}
	var dueDate *time.Time
	if body.DueDate != "" {
		if t, err := time.ParseInLocation("2006-01-02", body.DueDate, loc); err == nil {
			tt := time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
			dueDate = &tt
		}
	}

	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}

	dbc := db(c)
	taxCfg := tax.LoadFromDB(dbc)
	svc := service.NewSaleService(dbc)
	var centralTenantID uint
	if tenant, ok := c.Locals("tenant").(*database.Tenant); ok && tenant != nil {
		centralTenantID = tenant.ID
	}
	var issuedFromQuotationID *uint
	if body.FromQuotationID != nil && *body.FromQuotationID > 0 {
		// La cotización origen debe ser de la sucursal en la que se crea la venta (antes se podía
		// convertir —y consumir— la cotización de otra sucursal).
		if err := branchguard.QuotationBranchMatches(c, dbc, *body.FromQuotationID, branchID); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "cotización no encontrada"})
		}
		qSvc := quotationsvc.NewQuotationService(db(c))
		if _, err := qSvc.EnsureCanLinkToSale(*body.FromQuotationID); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		qid := *body.FromQuotationID
		issuedFromQuotationID = &qid
	}
	createInput := service.CreateSaleInput{
		BranchID:              branchID,
		ContactID:             body.ContactID,
		UserID:                userID(c),
		CashSessionID:         body.CashSessionID,
		SeriesID:              body.SeriesID,
		DocType:               body.DocType,
		IssueDate:             issueDate,
		DueDate:               dueDate,
		Currency:              body.Currency,
		OperationTypeCode:     body.OperationTypeCode,
		ExchangeRate:          body.ExchangeRate,
		PaymentMethod:         body.PaymentMethod,
		Payments:              body.Payments,
		PaymentConditionCode:  body.PaymentConditionCode,
		CreditInstallments:    body.CreditInstallments,
		Notes:                 body.Notes,
		Items:                 body.Items,
		GlobalDiscountMode:    body.GlobalDiscountMode,
		ApplyServiceCharge:    body.ApplyServiceCharge,
		GlobalDiscountValue:   body.GlobalDiscountValue,
		TaxConfig:             taxCfg,
		CentralTenantID:       centralTenantID,
		FiscalContext:         body.FiscalContext,
		Detraccion:            body.Detraccion,
		Export:                body.Export,
		Prepayment:            body.Prepayment,
		IssuedFromQuotationID: issuedFromQuotationID,
		UserCanOverridePrice:  userCanOverridePrice(c),
		IdempotencyKey:        body.IdempotencyKey,
	}
	// Cotización de origen: marcarla convertida DENTRO de la transacción de la venta (UPDATE
	// condicional). Antes se marcaba después y con el error ignorado: dos conversiones simultáneas
	// creaban dos ventas y una cotización "borrador" con venta emitida si el marcado fallaba.
	if issuedFromQuotationID != nil {
		qid := *issuedFromQuotationID
		createInput.OnCreatedTx = func(tx *gorm.DB, created *database.TenantSale) error {
			target := "nota_venta"
			var ser database.TenantDocumentSeries
			if tx.First(&ser, created.SeriesID).Error == nil {
				if code := strings.TrimSpace(ser.SunatCode); code == "01" || code == "03" {
					target = code
				}
			}
			return quotationsvc.ClaimForConversionTx(tx, qid, created.ID, target)
		}
	}
	sale, err := svc.Create(createInput)
	// Reintento de un cobro ya guardado: se responde con la venta original y NO se repiten los
	// efectos secundarios (conversión de cotización, encolado a SUNAT) que ya ocurrieron.
	replayed := errors.Is(err, service.ErrIdempotentReplay)
	if err != nil && !replayed {
		return saleCreateErrorResponse(c, err)
	}

	if !replayed {
		triggerAutoFiscalEnqueue(c, sale)
	}

	// Construir print_data para impresión inmediata
	items, _ := svc.GetItems(sale.ID)
	var printPayments []service.PrintPaymentInput
	if len(body.Payments) > 0 {
		for _, p := range body.Payments {
			printPayments = append(printPayments, service.PrintPaymentInput{
				Method:    p.Method,
				Amount:    p.Amount,
				Reference: p.Reference,
			})
		}
	} else if body.PaymentMethod != "" && sale.Total > 0 {
		printPayments = []service.PrintPaymentInput{{Method: body.PaymentMethod, Amount: sale.Total}}
	}
	printData, _ := service.BuildPrintData(dbc, sale, items, printPayments, "")

	resp := fiber.Map{
		"success":    true,
		"sale":       sale,
		"print_data": printData,
	}
	if replayed {
		resp["idempotent_replay"] = true
	}
	if body.FiscalContext != nil {
		if fiscalCtx, err := svc.GetFiscalContext(sale.ID); err == nil && fiscalCtx != nil {
			resp["fiscal_context"] = fiscalCtx
		}
	}
	return c.Status(fiber.StatusCreated).JSON(resp)
}

func (h *SaleHandler) DetailPage(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	tdb := db(c)
	svc := service.NewSaleService(tdb)
	sale, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).SendString("Venta no encontrada")
	}
	items, _ := svc.GetItems(sale.ID)

	// Cargar contacto
	var contact *database.TenantContact
	if sale.ContactID != nil {
		var ct database.TenantContact
		if tdb.First(&ct, *sale.ContactID).Error == nil {
			contact = &ct
		}
	}

	// Cargar factura electrónica si existe
	var invoice *database.TenantInvoice
	var inv database.TenantInvoice
	if tdb.Where("sale_id = ?", sale.ID).First(&inv).Error == nil {
		invoice = &inv
	}

	return c.Render("sales/detail", fiber.Map{
		"Title":      "Detalle de Venta — " + sale.Number,
		"UserEmail":  email(c),
		"TenantName": tenantName(c),
		"IsDev":      false,
		"Sale":       sale,
		"Items":      items,
		"Contact":    contact,
		"Invoice":    invoice,
	}, "layouts/base")
}

func tenantName(c fiber.Ctx) string {
	if t, ok := c.Locals("tenant").(*database.Tenant); ok && t != nil {
		return t.Name
	}
	return ""
}

func (h *SaleHandler) CancelForm(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	if err := service.NewSaleService(db(c)).CancelNotaVenta(uint(id), userID(c), "Anulación desde panel web"); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}
	return c.Redirect().To("/sales?success=cancelled")
}

// CancelAPI POST /api/sales/:id/cancel — anula nota de venta (revierte caja, stock y totales).
func (h *SaleHandler) CancelAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	if err := branchguard.SaleWritable(c, db(c), uint(id)); err != nil {
		return saleNotFound(c)
	}
	if err := service.NewSaleService(db(c)).CancelNotaVenta(uint(id), userID(c), strings.TrimSpace(body.Reason)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "message": "Venta anulada correctamente"})
}

// resolveSalesReportBranch sucursal de un listado/reporte de ventas. branch_id=all pide "todas las
// sucursales" de forma explícita (el selector de los reportes, que antes con el valor vacío caía en
// la sucursal activa); un número filtra esa sucursal; sin valor, la sucursal activa como siempre.
// Quien no puede cambiar de sucursal queda siempre en la suya (ver branch.ResolveReportBranchFilter).
func resolveSalesReportBranch(c fiber.Ctx) uint {
	raw := strings.TrimSpace(c.Query("branch_id"))
	if strings.EqualFold(raw, "all") {
		return branch.ResolveReportBranchFilter(c, 0, true)
	}
	req, _ := strconv.ParseUint(raw, 10, 32)
	return branch.ResolveReadBranchFilter(c, uint(req))
}

// VoidRejectedAPI POST /api/sales/:id/void-rejected — anula localmente una factura o boleta que SUNAT
// rechazó: revierte caja, bancos, stock, seriales y anticipos (ver BillingService.VoidRejectedSale).
func (h *SaleHandler) VoidRejectedAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	if err := branchguard.SaleWritable(c, db(c), uint(id)); err != nil {
		return saleNotFound(c)
	}
	svc := billingSvc.NewBillingService(db(c))
	if t, ok := c.Locals("tenant").(*database.Tenant); ok && t != nil {
		svc.SetCentralTenantID(t.ID)
		svc.SetTenantSlug(t.Slug)
	}
	if err := svc.VoidRejectedSale(billingSvc.VoidRejectedInput{
		SaleID:     uint(id),
		Reason:     body.Reason,
		ActorID:    userID(c),
		ActorEmail: email(c),
		ClientIP:   c.IP(),
	}); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "message": "Venta rechazada anulada correctamente"})
}

// GET /api/sales/filter-users — usuarios para los filtros "Registrado por" y "Vendedor" de la lista y el
// reporte de ventas. Solo id y nombre, bajo el acceso de ver ventas (no exige users.view: son filtros, no
// administración). Incluye usuarios eliminados porque sus ventas siguen existiendo.
func (h *SaleHandler) FilterUsersAPI(c fiber.Ctx) error {
	var rows []struct {
		ID      uint   `json:"id"`
		Name    string `json:"name"`
		Deleted bool   `json:"deleted"`
	}
	if err := db(c).Unscoped().Table("tenant_users").
		Select("id, name, (deleted_at IS NOT NULL) AS deleted").
		Order("deleted_at IS NOT NULL, name ASC").Scan(&rows).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// GET /api/sales?q=&from=&to=&doc_type=&billing_status=&sunat_code=00|01,03&contact_id=
func (h *SaleHandler) ListAPI(c fiber.Ctx) error {
	// Un tenant no puede tener más de 2 listados/reportes simultáneos: su pool tiene pocas
	// conexiones y el login/middleware comparten la misma (ver pkg/reportgate).
	gateKey := "?"
	if t, ok := tenantctx.Tenant(c); ok && t != nil {
		gateKey = t.Slug
	}
	release, ok := saleListGate.Acquire(gateKey, saleListGateWait)
	if !ok {
		c.Set("Retry-After", "5")
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": "Hay otros reportes de ventas en curso. Espera unos segundos e intenta de nuevo.",
		})
	}
	defer release()

	svc := service.NewSaleService(db(c))
	branchID := resolveSalesReportBranch(c)
	contactID, _ := strconv.ParseUint(c.Query("contact_id"), 10, 32)
	var dateFrom, dateTo *time.Time
	if from := c.Query("from"); from != "" {
		if t, err := time.ParseInLocation("2006-01-02", from, time.Local); err == nil {
			start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
			dateFrom = &start
		}
	}
	if to := c.Query("to"); to != "" {
		if t, err := time.ParseInLocation("2006-01-02", to, time.Local); err == nil {
			end := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.Local)
			dateTo = &end
		}
	}
	var sunatCodes []string
	if sc := strings.TrimSpace(c.Query("sunat_code")); sc != "" {
		for _, part := range strings.Split(sc, ",") {
			if code := strings.TrimSpace(part); code != "" {
				sunatCodes = append(sunatCodes, code)
			}
		}
	}
	userIDFilter, _ := strconv.ParseUint(c.Query("user_id"), 10, 32)
	sellerIDFilter, _ := strconv.ParseUint(c.Query("seller_user_id"), 10, 32)
	params := service.SaleListParams{
		BranchID:      uint(branchID),
		ContactID:     uint(contactID),
		DocType:       c.Query("doc_type"),
		Status:        c.Query("status"),
		BillingStatus: c.Query("billing_status"),
		PaymentMethod: c.Query("payment_method"),
		PaymentMode:   c.Query("payment_mode"),
		UserID:        uint(userIDFilter),
		SellerUserID:  uint(sellerIDFilter),
		Query:         c.Query("q"),
		DateFrom:      dateFrom,
		DateTo:        dateTo,
		SunatCodes:    sunatCodes,
		// scope=commercial: el reporte de ventas cuenta lo mismo que el dashboard (ver
		// SaleListParams.CommercialReport). Sin él, el listado conserva todas las filas.
		CommercialReport: c.Query("scope") == "commercial",
	}
	switch strings.TrimSpace(c.Query("sale_status")) {
	case "active":
		params.CancelledFilter = "exclude"
	case "cancelled":
		params.CancelledFilter = "only"
	}
	exportAll := c.Query("export_all") == "1" || c.Query("export_all") == "true"
	perPage, _ := strconv.Atoi(c.Query("per_page"))
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	if exportAll {
		// Exportación / descarga completa: mismos filtros, sin paginar (ignora page y per_page).
		params.Limit = 0
		params.Offset = 0
	} else if perPage > 0 {
		params.Limit = perPage
		params.Offset = (page - 1) * perPage
	}
	// summary=0: la pantalla solo muestra filas y no necesita los totales (evita ~8 sentencias).
	params.SkipSummary = c.Query("summary") == "0"
	sales, total, summary, err := svc.List(params)
	if err != nil {
		if errors.Is(err, service.ErrSaleListTimeout) {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	saleIDs := make([]uint, 0, len(sales))
	for _, s := range sales {
		if s.DocType == "01" || s.DocType == "03" {
			saleIDs = append(saleIDs, s.ID)
		}
	}
	linkedBySale := billingSvc.NewBillingService(db(c)).BatchLinkedPerceptionsBySourceSaleIDs(saleIDs)
	type saleListRow struct {
		database.TenantSale
		LinkedPerception *billingSvc.LinkedFiscalDocSummary `json:"linked_perception,omitempty"`
	}
	rows := make([]saleListRow, len(sales))
	for i, s := range sales {
		rows[i] = saleListRow{TenantSale: s}
		if lp, ok := linkedBySale[s.ID]; ok {
			summary := lp
			rows[i].LinkedPerception = &summary
		}
	}
	if exportAll {
		return c.JSON(fiber.Map{"data": rows, "summary": summary})
	}
	if perPage > 0 {
		return c.JSON(fiber.Map{"data": rows, "total": total, "summary": summary})
	}
	return c.JSON(fiber.Map{"data": rows, "summary": summary})
}

// GET /api/sales/by-product?from=&to=&branch_id=&category_id=
func (h *SaleHandler) ListByProductAPI(c fiber.Ctx) error {
	svc := service.NewSaleService(db(c))
	branchID := resolveSalesReportBranch(c)
	catID, _ := strconv.ParseUint(c.Query("category_id"), 10, 32)
	var dateFrom, dateTo *time.Time
	if from := c.Query("from"); from != "" {
		if t, err := time.ParseInLocation("2006-01-02", from, time.Local); err == nil {
			start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
			dateFrom = &start
		}
	}
	if to := c.Query("to"); to != "" {
		if t, err := time.ParseInLocation("2006-01-02", to, time.Local); err == nil {
			end := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.Local)
			dateTo = &end
		}
	}
	params := service.SalesByProductParams{
		DateFrom:    dateFrom,
		DateTo:      dateTo,
		BranchID:    uint(branchID),
		CategoryID:  uint(catID),
		Q:           c.Query("q"),
		ProductType: c.Query("product_type"),
	}
	rows, summary, err := svc.SalesByProduct(params)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows, "summary": summary})
}

// GET /api/sales/profit-detail?from=&to=&branch_id=&category_id=&q=
func (h *SaleHandler) ListProfitDetailAPI(c fiber.Ctx) error {
	svc := service.NewSaleService(db(c))
	branchID := resolveSalesReportBranch(c)
	catID, _ := strconv.ParseUint(c.Query("category_id"), 10, 32)
	var dateFrom, dateTo *time.Time
	if from := c.Query("from"); from != "" {
		if t, err := time.ParseInLocation("2006-01-02", from, time.Local); err == nil {
			start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
			dateFrom = &start
		}
	}
	if to := c.Query("to"); to != "" {
		if t, err := time.ParseInLocation("2006-01-02", to, time.Local); err == nil {
			end := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.Local)
			dateTo = &end
		}
	}
	params := service.ProfitDetailParams{
		DateFrom:   dateFrom,
		DateTo:     dateTo,
		BranchID:   uint(branchID),
		CategoryID: uint(catID),
		Q:          c.Query("q"),
	}
	rows, summary, err := svc.ProfitDetail(params)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows, "summary": summary})
}

func (h *SaleHandler) GetAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewSaleService(db(c))
	sale, err := svc.GetByID(uint(id))
	if err != nil || !branch.RecordReadable(c, sale.BranchID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No encontrado"})
	}
	enriched := []database.TenantSale{*sale}
	nvdisplay.EnrichSales(db(c), enriched)
	sale = &enriched[0]
	if sale.ContactID != nil && *sale.ContactID > 0 {
		var contact database.TenantContact
		if db(c).First(&contact, *sale.ContactID).Error == nil {
			sale.ContactName = contact.BusinessName
		}
	}
	items, _ := svc.GetItems(sale.ID)
	payments, _ := svc.GetPayments(sale.ID)
	creditInstallments, _ := svc.GetCreditInstallments(sale.ID)
	out := fiber.Map{"sale": sale, "items": items, "payments": payments, "credit_installments": creditInstallments}
	if sale.ContactID != nil && *sale.ContactID > 0 {
		var contact database.TenantContact
		if db(c).First(&contact, *sale.ContactID).Error == nil {
			out["contact"] = fiber.Map{
				"id":            contact.ID,
				"doc_type":      contact.DocType,
				"doc_number":    contact.DocNumber,
				"business_name": contact.BusinessName,
				"trade_name":    contact.TradeName,
				"phone":         strings.TrimSpace(contact.Phone),
				// address/ubigeo: los necesita DespatchFormModal.tsx al armar la guía de remisión
				// "desde esta venta" (buildDespatchPrefillFromSaleDetail) — sin esto, el destinatario
				// siempre quedaba con ubigeo vacío aunque el contacto lo tuviera bien guardado.
				"address": strings.TrimSpace(contact.Address),
				"ubigeo":  strings.TrimSpace(contact.Ubigeo),
			}
		}
	}
	if inv, _ := billingSvc.NewBillingService(db(c)).GetInvoice(sale.ID); inv != nil {
		out["invoice"] = inv
	}
	if printData, err := service.BuildPrintDataForSale(db(c), sale.ID); err == nil {
		out["print_data"] = printData
	}
	if fiscalCtx, err := svc.GetFiscalContext(sale.ID); err == nil && fiscalCtx != nil {
		out["fiscal_context"] = fiscalCtx
	}
	if det, err := detraccionsvc.NewService(db(c)).LoadBySaleID(sale.ID); err == nil && det != nil {
		out["detraccion"] = det
	}
	if linked, _ := billingSvc.NewBillingService(db(c)).GetLinkedPerceptionBySourceSaleID(sale.ID); linked != nil {
		out["linked_perception"] = linked
	}
	return c.JSON(out)
}

// EmailReceiptAPI POST /api/sales/:id/email-receipt — envía PDF ticket al correo del cliente.
func (h *SaleHandler) EmailReceiptAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Email     string `json:"email"`
		PdfBase64 string `json:"pdf_base64"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	if err := branchguard.SaleReadable(c, db(c), uint(id)); err != nil {
		return saleNotFound(c)
	}
	svc := service.NewSaleService(db(c))
	if err := svc.EmailReceipt(uint(id), service.EmailReceiptInput{
		Email:     body.Email,
		PdfBase64: body.PdfBase64,
	}); err != nil {
		if errors.Is(err, emailpkg.ErrNotConfigured) {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error": "El envío por correo no está configurado (SMTP_HOST)",
				"code":  "SMTP_NOT_CONFIGURED",
			})
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// IssueElectronicFromNotaAPI POST /api/sales/:id/issue-electronic — factura/boleta desde nota de venta (00).
func (h *SaleHandler) IssueElectronicFromNotaAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		SeriesID  uint   `json:"series_id"`
		IssueDate string `json:"issue_date"`
		ContactID *uint  `json:"contact_id"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	if body.SeriesID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "series_id es obligatorio"})
	}
	if err := branchguard.SaleWritable(c, db(c), uint(id)); err != nil {
		return saleNotFound(c)
	}
	svc := service.NewSaleService(db(c))
	var centralTenantID uint
	if tenant, ok := c.Locals("tenant").(*database.Tenant); ok && tenant != nil {
		centralTenantID = tenant.ID
	}
	sale, err := svc.IssueElectronicFromNota(uint(id), body.SeriesID, userID(c), body.IssueDate, centralTenantID, body.ContactID)
	if err != nil {
		return saleCreateErrorResponse(c, err)
	}
	triggerAutoFiscalEnqueue(c, sale)
	out := fiber.Map{"sale": sale}
	if printData, err := service.BuildPrintDataForSale(db(c), sale.ID); err == nil {
		out["print_data"] = printData
	}
	return c.Status(fiber.StatusCreated).JSON(out)
}

func saleCreateErrorResponse(c fiber.Ctx, err error) error {
	st := fiber.StatusBadRequest
	payload := fiber.Map{"error": err.Error()}
	if errors.Is(err, docusage.ErrQuotaExceeded) {
		st = fiber.StatusPaymentRequired
		payload["code"] = "DOCUMENT_QUOTA_EXCEEDED"
	}
	return c.Status(st).JSON(payload)
}

func triggerAutoFiscalEnqueue(c fiber.Ctx, sale *database.TenantSale) {
	if sale == nil || sale.ID == 0 {
		return
	}
	tenant, ok := c.Locals("tenant").(*database.Tenant)
	if !ok || tenant == nil {
		return
	}
	_ = billingSvc.TriggerAutoEnqueueAfterSaleCommit(db(c), tenant, sale.ID)
}

// Crear venta desde formulario clásico (no POS)
func (h *SaleHandler) NewPage(c fiber.Ctx) error {
	tdb := db(c)
	var branches []database.TenantBranch
	tdb.Where("active = ?", true).Find(&branches)
	var allSeries []database.TenantDocumentSeries
	tdb.Where("active = ?", true).Order("doc_type, series").Find(&allSeries)
	var contacts []database.TenantContact
	tdb.Where("active = ? AND (type = 'customer' OR type = 'both')", true).Order("business_name").Find(&contacts)
	var products []database.TenantProduct
	tdb.Where("active = ?", true).Order("name").Find(&products)

	return c.Render("sales/form", fiber.Map{
		"Title":     "Nueva Venta",
		"UserEmail": email(c),
		"Branches":  branches,
		"Series":    allSeries,
		"Contacts":  contacts,
		"Products":  products,
	}, "layouts/base")
}

func (h *SaleHandler) CreateForm(c fiber.Ctx) error {
	branchID, _ := strconv.ParseUint(c.FormValue("branch_id"), 10, 32)
	seriesID, _ := strconv.ParseUint(c.FormValue("series_id"), 10, 32)
	var contactID *uint
	if cid, err := strconv.ParseUint(c.FormValue("contact_id"), 10, 32); err == nil && cid > 0 {
		v := uint(cid)
		contactID = &v
	}
	loc, err := time.LoadLocation("America/Lima")
	if err != nil || loc == nil {
		loc = time.Local
	}
	nowPe := time.Now().In(loc)
	issueDate := time.Date(nowPe.Year(), nowPe.Month(), nowPe.Day(), 12, 0, 0, 0, loc)
	if d := c.FormValue("issue_date"); d != "" {
		if t, err := time.ParseInLocation("2006-01-02", d, loc); err == nil {
			issueDate = time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
		}
	}

	// Parsear ítems del JSON
	var items []service.SaleItemInput
	itemsJSON := c.FormValue("items_json")
	if itemsJSON != "" {
		json.Unmarshal([]byte(itemsJSON), &items)
	}

	svc := service.NewSaleService(db(c))
	sale, err := svc.Create(service.CreateSaleInput{
		BranchID:             uint(branchID),
		ContactID:            contactID,
		UserID:               userID(c),
		SeriesID:             uint(seriesID),
		DocType:              c.FormValue("doc_type"),
		IssueDate:            issueDate,
		Currency:             c.FormValue("currency"),
		PaymentMethod:        c.FormValue("payment_method"),
		Notes:                c.FormValue("notes"),
		Items:                items,
		UserCanOverridePrice: userCanOverridePrice(c),
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}
	return c.Redirect().To("/sales/" + strconv.FormatUint(uint64(sale.ID), 10))
}

// PendingRefundsAPI devoluciones de dinero que quedaron sin registrar al anular ventas.
//
// Se derivan de los cobros sin reversión (reversal_of_id), así que no hay estado que mantener:
// aplicar la devolución las quita de la lista sola.
func (h *SaleHandler) PendingRefundsAPI(c fiber.Ctx) error {
	branchID := branch.ActiveBranchID(c)
	if reqB, err := strconv.ParseUint(c.Query("branch_id"), 10, 32); err == nil && reqB > 0 {
		branchID = branch.ResolveReadBranchFilter(c, uint(reqB))
	}
	rows, err := service.NewSaleService(db(c)).PendingSaleRefunds(branchID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// ApplyPendingRefundAPI registra una devolución pendiente en la caja indicada.
func (h *SaleHandler) ApplyPendingRefundAPI(c fiber.Ctx) error {
	var body struct {
		CashMovementID uint   `json:"cash_movement_id"`
		CashSessionID  uint   `json:"cash_session_id"`
		Reason         string `json:"reason"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	if body.CashMovementID == 0 || body.CashSessionID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "movimiento y caja son requeridos"})
	}
	// El movimiento original (y por tanto la venta anulada) y la caja destino deben ser de la
	// sucursal activa: antes cualquier usuario podía sacar dinero de una caja de otra sucursal.
	if err := branchguard.CashMovementWritable(c, db(c), body.CashMovementID); err != nil {
		return saleNotFound(c)
	}
	if err := branchguard.SessionWritable(c, db(c), body.CashSessionID); err != nil {
		return saleNotFound(c)
	}
	err := service.NewSaleService(db(c)).ApplyPendingRefund(
		body.CashMovementID, body.CashSessionID, userID(c), strings.TrimSpace(body.Reason),
	)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "message": "Devolución registrada en caja"})
}

// ApplyPendingNoteRefundAPI registra en caja la devolución del monto de una nota de crédito
// parcial (Fase 2) — contraparte de ApplyPendingRefundAPI para devoluciones que no vienen de
// anular una venta completa. Ver PendingRefund.Source en PendingRefundsAPI.
func (h *SaleHandler) ApplyPendingNoteRefundAPI(c fiber.Ctx) error {
	var body struct {
		NoteSaleID    uint   `json:"note_sale_id"`
		CashSessionID uint   `json:"cash_session_id"`
		Reason        string `json:"reason"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	if body.NoteSaleID == 0 || body.CashSessionID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "nota y caja son requeridas"})
	}
	if err := branchguard.SaleWritable(c, db(c), body.NoteSaleID); err != nil {
		return saleNotFound(c)
	}
	if err := branchguard.SessionWritable(c, db(c), body.CashSessionID); err != nil {
		return saleNotFound(c)
	}
	err := service.NewSaleService(db(c)).ApplyPendingNoteRefund(
		body.NoteSaleID, body.CashSessionID, userID(c), strings.TrimSpace(body.Reason),
	)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "message": "Devolución registrada en caja"})
}
