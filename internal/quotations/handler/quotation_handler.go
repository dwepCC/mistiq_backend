package handler

import (
	"errors"
	"strconv"
	"strings"
	"time"

	billingSvc "tukifac/internal/billing/service"
	quotationsvc "tukifac/internal/quotations/service"
	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	emailpkg "tukifac/pkg/email"
	"tukifac/pkg/tax"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type QuotationHandler struct{}

func NewQuotationHandler() *QuotationHandler { return &QuotationHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

func userID(c fiber.Ctx) uint {
	v, _ := c.Locals("user_id").(uint)
	return v
}

func centralTenantID(c fiber.Ctx) uint {
	if tenant, ok := c.Locals("tenant").(*database.Tenant); ok && tenant != nil {
		return tenant.ID
	}
	return 0
}

func parseIssueDatePe(bodyDate string) time.Time {
	loc, err := time.LoadLocation("America/Lima")
	if err != nil || loc == nil {
		loc = time.Local
	}
	nowPe := time.Now().In(loc)
	fallback := time.Date(nowPe.Year(), nowPe.Month(), nowPe.Day(), 12, 0, 0, 0, loc)
	if strings.TrimSpace(bodyDate) == "" {
		return fallback
	}
	if t, err := time.ParseInLocation("2006-01-02", bodyDate, loc); err == nil {
		return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
	}
	return fallback
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

// GET /api/quotations
func (h *QuotationHandler) ListAPI(c fiber.Ctx) error {
	svc := quotationsvc.NewQuotationService(db(c))
	branchID, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	params := quotationsvc.QuotationListParams{
		BranchID: uint(branchID),
		Query:    c.Query("q"),
		Status:   strings.TrimSpace(c.Query("status")),
		Limit:    limit,
		Offset:   offset,
	}
	if from := strings.TrimSpace(c.Query("from")); from != "" {
		if t := quotationsvc.ParseOptionalDateYMD(from); t != nil {
			params.From = *t
		}
	}
	if to := strings.TrimSpace(c.Query("to")); to != "" {
		if t := quotationsvc.ParseOptionalDateYMD(to); t != nil {
			params.To = *t
		}
	}
	rows, total, err := svc.List(params)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows, "total": total})
}

// GET /api/quotations/:id
func (h *QuotationHandler) GetAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := quotationsvc.NewQuotationService(db(c))
	q, items, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	out := fiber.Map{"quotation": q, "items": items}
	if printData, err := quotationsvc.BuildPrintDataForQuotation(db(c), uint(id)); err == nil {
		out["print_data"] = printData
	}
	return c.JSON(out)
}

// POST /api/quotations
func (h *QuotationHandler) CreateAPI(c fiber.Ctx) error {
	var body struct {
		BranchID     uint                              `json:"branch_id"`
		ContactID    *uint                             `json:"contact_id"`
		SeriesID     uint                              `json:"series_id"`
		IssueDate    string                            `json:"issue_date"`
		ValidUntil   string                            `json:"valid_until"`
		Currency     string                            `json:"currency"`
		ExchangeRate *float64                          `json:"exchange_rate"`
		Notes               string                            `json:"notes"`
		ShowTermsConditions bool                              `json:"show_terms_conditions"`
		Items               []quotationsvc.QuotationItemInput `json:"items"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	taxCfg := tax.LoadFromDB(db(c))
	svc := quotationsvc.NewQuotationService(db(c))
	q, err := svc.Create(quotationsvc.CreateQuotationInput{
		BranchID:     branchID,
		ContactID:    body.ContactID,
		UserID:       userID(c),
		SeriesID:     body.SeriesID,
		IssueDate:    parseIssueDatePe(body.IssueDate),
		ValidUntil:   quotationsvc.ParseOptionalDateYMD(body.ValidUntil),
		Currency:     body.Currency,
		ExchangeRate: body.ExchangeRate,
		Notes:               body.Notes,
		ShowTermsConditions: body.ShowTermsConditions,
		Items:               body.Items,
		TaxConfig:    taxCfg,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	out := fiber.Map{"quotation": q}
	if printData, err := quotationsvc.BuildPrintDataForQuotation(db(c), q.ID); err == nil {
		out["print_data"] = printData
	}
	return c.Status(fiber.StatusCreated).JSON(out)
}

// PATCH /api/quotations/:id
func (h *QuotationHandler) UpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		ContactID    *uint                             `json:"contact_id"`
		SeriesID     uint                              `json:"series_id"`
		IssueDate    string                            `json:"issue_date"`
		ValidUntil   string                            `json:"valid_until"`
		Currency     string                            `json:"currency"`
		ExchangeRate *float64                          `json:"exchange_rate"`
		Notes               string                            `json:"notes"`
		ShowTermsConditions bool                              `json:"show_terms_conditions"`
		Items               []quotationsvc.QuotationItemInput `json:"items"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	taxCfg := tax.LoadFromDB(db(c))
	svc := quotationsvc.NewQuotationService(db(c))
	q, err := svc.Update(uint(id), quotationsvc.UpdateQuotationInput{
		ContactID:    body.ContactID,
		SeriesID:     body.SeriesID,
		IssueDate:    parseIssueDatePe(body.IssueDate),
		ValidUntil:   quotationsvc.ParseOptionalDateYMD(body.ValidUntil),
		Currency:     body.Currency,
		ExchangeRate: body.ExchangeRate,
		Notes:               body.Notes,
		ShowTermsConditions: body.ShowTermsConditions,
		Items:               body.Items,
		TaxConfig:    taxCfg,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	out := fiber.Map{"quotation": q}
	if printData, err := quotationsvc.BuildPrintDataForQuotation(db(c), q.ID); err == nil {
		out["print_data"] = printData
	}
	return c.JSON(out)
}

// DELETE /api/quotations/:id
func (h *QuotationHandler) DeleteAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := quotationsvc.NewQuotationService(db(c))
	if err := svc.Delete(uint(id)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/quotations/:id/convert — conversión directa (opción A).
func (h *QuotationHandler) ConvertAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Target    string `json:"target"`
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
	svc := quotationsvc.NewQuotationService(db(c))
	sale, err := svc.ConvertToSale(uint(id), quotationsvc.ConvertInput{
		Target:        body.Target,
		SeriesID:      body.SeriesID,
		IssueDate:     parseIssueDatePe(body.IssueDate),
		ContactID:     body.ContactID,
		UserID:        userID(c),
		CentralTenant: centralTenantID(c),
		TaxConfig:     tax.LoadFromDB(db(c)),
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	triggerAutoFiscalEnqueue(c, sale)
	out := fiber.Map{"sale": sale}
	if printData, err := salessvc.BuildPrintDataForSale(db(c), sale.ID); err == nil {
		out["print_data"] = printData
	}
	return c.JSON(out)
}

// POST /api/quotations/:id/email-receipt — envía PDF de cotización al correo del cliente.
func (h *QuotationHandler) EmailReceiptAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Email     string `json:"email"`
		PdfBase64 string `json:"pdf_base64"`
		Format    string `json:"format"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	svc := quotationsvc.NewQuotationService(db(c))
	if err := svc.EmailQuotation(uint(id), quotationsvc.EmailQuotationInput{
		Email:     body.Email,
		PdfBase64: body.PdfBase64,
		Format:    body.Format,
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
