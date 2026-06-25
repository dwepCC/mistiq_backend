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
	quotationsvc "tukifac/internal/quotations/service"
	"tukifac/internal/sales/nvdisplay"
	"tukifac/internal/sales/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	emailpkg "tukifac/pkg/email"
	"tukifac/pkg/saas/docusage"
	"tukifac/pkg/tax"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type SaleHandler struct{}

func NewSaleHandler() *SaleHandler { return &SaleHandler{} }

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
		BranchID      uint                     `json:"branch_id"`
		ContactID     *uint                    `json:"contact_id"`
		CashSessionID *uint                    `json:"cash_session_id"`
		SeriesID      uint                     `json:"series_id"`
		DocType       string                   `json:"doc_type"`
		IssueDate     string                   `json:"issue_date"`
		DueDate       string                   `json:"due_date"`
		Currency          string                   `json:"currency"`
		OperationTypeCode string                   `json:"operation_type_code"`
		ExchangeRate      *float64                 `json:"exchange_rate"`
		PaymentMethod     string                   `json:"payment_method"`
		Payments      []service.PaymentInput   `json:"payments"`
		Notes         string                   `json:"notes"`
		Items         []service.SaleItemInput  `json:"items"`
		FiscalContext *salecontext.FiscalContextInput `json:"fiscal_context"`
		Detraccion    *detraccionsvc.SaleInput        `json:"detraccion"`
		FromQuotationID *uint                         `json:"from_quotation_id"`
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

	taxCfg := tax.LoadFromDB(db(c))
	svc := service.NewSaleService(db(c))
	var centralTenantID uint
	if tenant, ok := c.Locals("tenant").(*database.Tenant); ok && tenant != nil {
		centralTenantID = tenant.ID
	}
	var issuedFromQuotationID *uint
	if body.FromQuotationID != nil && *body.FromQuotationID > 0 {
		qSvc := quotationsvc.NewQuotationService(db(c))
		if _, err := qSvc.EnsureCanLinkToSale(*body.FromQuotationID); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		qid := *body.FromQuotationID
		issuedFromQuotationID = &qid
	}
	sale, err := svc.Create(service.CreateSaleInput{
		BranchID:        branchID,
		ContactID:       body.ContactID,
		UserID:          userID(c),
		CashSessionID:   body.CashSessionID,
		SeriesID:        body.SeriesID,
		DocType:         body.DocType,
		IssueDate:       issueDate,
		DueDate:         dueDate,
		Currency:          body.Currency,
		OperationTypeCode: body.OperationTypeCode,
		ExchangeRate:      body.ExchangeRate,
		PaymentMethod:     body.PaymentMethod,
		Payments:        body.Payments,
		Notes:           body.Notes,
		Items:           body.Items,
		TaxConfig:       taxCfg,
		CentralTenantID: centralTenantID,
		FiscalContext:           body.FiscalContext,
		Detraccion:              body.Detraccion,
		IssuedFromQuotationID:   issuedFromQuotationID,
	})
	if err != nil {
		return saleCreateErrorResponse(c, err)
	}
	if body.FromQuotationID != nil && *body.FromQuotationID > 0 {
		target := "nota_venta"
		var ser database.TenantDocumentSeries
		if db(c).First(&ser, sale.SeriesID).Error == nil {
			code := strings.TrimSpace(ser.SunatCode)
			if code == "01" || code == "03" {
				target = code
			}
		}
		_ = quotationsvc.NewQuotationService(db(c)).MarkConverted(*body.FromQuotationID, sale.ID, target)
	}

	triggerAutoFiscalEnqueue(c, sale)

	// Construir print_data para impresión inmediata
	items, _ := svc.GetItems(sale.ID)
	var printPayments []service.PrintPaymentInput
	if len(body.Payments) > 0 {
		for _, p := range body.Payments {
			printPayments = append(printPayments, service.PrintPaymentInput{Method: p.Method, Amount: p.Amount})
		}
	} else if body.PaymentMethod != "" && sale.Total > 0 {
		printPayments = []service.PrintPaymentInput{{Method: body.PaymentMethod, Amount: sale.Total}}
	}
	printData, _ := service.BuildPrintData(db(c), sale, items, printPayments, "")

	resp := fiber.Map{
		"success":    true,
		"sale":       sale,
		"print_data": printData,
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
	if err := service.NewSaleService(db(c)).CancelNotaVenta(uint(id), userID(c), strings.TrimSpace(body.Reason)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "message": "Venta anulada correctamente"})
}

// GET /api/sales?q=&from=&to=&doc_type=&billing_status=&sunat_code=00|01,03&contact_id=
func (h *SaleHandler) ListAPI(c fiber.Ctx) error {
	svc := service.NewSaleService(db(c))
	reqBranch, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(reqBranch))
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
	params := service.SaleListParams{
		BranchID:      uint(branchID),
		ContactID:     uint(contactID),
		DocType:       c.Query("doc_type"),
		Status:        c.Query("status"),
		BillingStatus: c.Query("billing_status"),
		PaymentMethod: c.Query("payment_method"),
		PaymentMode:   c.Query("payment_mode"),
		Query:         c.Query("q"),
		DateFrom:      dateFrom,
		DateTo:        dateTo,
		SunatCodes:    sunatCodes,
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
	sales, total, summary, err := svc.List(params)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if exportAll {
		return c.JSON(fiber.Map{"data": sales, "summary": summary})
	}
	if perPage > 0 {
		return c.JSON(fiber.Map{"data": sales, "total": total, "summary": summary})
	}
	return c.JSON(fiber.Map{"data": sales, "summary": summary})
}

// GET /api/sales/by-product?from=&to=&branch_id=&category_id=
func (h *SaleHandler) ListByProductAPI(c fiber.Ctx) error {
	svc := service.NewSaleService(db(c))
	reqBranch, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(reqBranch))
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
		DateFrom:   dateFrom,
		DateTo:     dateTo,
		BranchID:   uint(branchID),
		CategoryID: uint(catID),
	}
	rows, summary, err := svc.SalesByProduct(params)
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
	if err != nil {
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
	out := fiber.Map{"sale": sale, "items": items, "payments": payments}
	if sale.ContactID != nil && *sale.ContactID > 0 {
		var contact database.TenantContact
		if db(c).First(&contact, *sale.ContactID).Error == nil {
			out["contact"] = fiber.Map{
				"id":             contact.ID,
				"doc_type":       contact.DocType,
				"doc_number":     contact.DocNumber,
				"business_name":  contact.BusinessName,
				"trade_name":     contact.TradeName,
				"phone":          strings.TrimSpace(contact.Phone),
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
		BranchID:      uint(branchID),
		ContactID:     contactID,
		UserID:        userID(c),
		SeriesID:      uint(seriesID),
		DocType:       c.FormValue("doc_type"),
		IssueDate:     issueDate,
		Currency:      c.FormValue("currency"),
		PaymentMethod: c.FormValue("payment_method"),
		Notes:         c.FormValue("notes"),
		Items:         items,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}
	return c.Redirect().To("/sales/" + strconv.FormatUint(uint64(sale.ID), 10))
}
