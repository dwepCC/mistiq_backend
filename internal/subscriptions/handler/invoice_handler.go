package handler

import (
	"strconv"

	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
)

type renewalInvoiceBody struct {
	TenantID uint    `json:"tenant_id"`
	Months   int     `json:"months"`
	Amount   float64 `json:"amount"`
	Notes    string  `json:"notes"`
}

func invoiceRow(c saas.InvoiceRow) fiber.Map {
	return fiber.Map{
		"id":           c.ID,
		"tenant_id":    c.TenantID,
		"period_start": c.PeriodStart,
		"period_end":   c.PeriodEnd,
		"due_date":     c.DueDate,
		"amount":       c.Amount,
		"currency":     c.Currency,
		"status":       c.Status,
		"paid_at":      c.PaidAt,
	}
}

func actorID(c fiber.Ctx) *uint {
	saUserID, _ := c.Locals("sa_user_id").(uint)
	if saUserID == 0 {
		return nil
	}
	return &saUserID
}

// GET /api/superadmin/billing-cycles/preview?tenant_id=&months=&amount=
// Muestra qué se cobraría (período, vencimiento e importe) sin escribir nada.
func (h *SubscriptionHandler) PreviewInvoiceAPI(c fiber.Ctx) error {
	tenantID, _ := strconv.ParseUint(c.Query("tenant_id"), 10, 32)
	months, _ := strconv.Atoi(c.Query("months"))
	amount, _ := strconv.ParseFloat(c.Query("amount"), 64)

	preview, err := saas.PreviewRenewalInvoice(saas.RenewalInvoiceInput{
		TenantID: uint(tenantID),
		Months:   months,
		Amount:   amount,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": preview})
}

// POST /api/superadmin/billing-cycles — emite el cobro de la próxima renovación.
func (h *SubscriptionHandler) CreateInvoiceAPI(c fiber.Ctx) error {
	if err := requireSuperAdminRole(c); err != nil {
		return err
	}
	var body renewalInvoiceBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}

	cycle, err := saas.IssueRenewalInvoice(saas.RenewalInvoiceInput{
		TenantID: body.TenantID,
		Months:   body.Months,
		Amount:   body.Amount,
		Notes:    body.Notes,
		ActorID:  actorID(c),
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true, "data": invoiceRow(saas.ToInvoiceRow(cycle))})
}

// GET /api/superadmin/tenants/:id/billing-cycles — cobros del tenant.
func (h *SubscriptionHandler) ListInvoicesAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	rows, err := saas.ListTenantInvoices(uint(id), limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	out := make([]fiber.Map, 0, len(rows))
	for i := range rows {
		out = append(out, invoiceRow(saas.ToInvoiceRow(&rows[i])))
	}
	return c.JSON(fiber.Map{"data": out})
}

// PATCH /api/superadmin/billing-cycles/:id/cancel — anula un cobro no pagado.
func (h *SubscriptionHandler) CancelInvoiceAPI(c fiber.Ctx) error {
	if err := requireSuperAdminRole(c); err != nil {
		return err
	}
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := saas.CancelInvoice(uint(id)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/superadmin/billing-cycles?status=&limit= — cobros de todas las empresas.
//
// Sin status trae solo los que siguen por cobrar, que es lo que hay que revisar.
func (h *SubscriptionHandler) ListAllInvoicesAPI(c fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit"))
	rows, err := saas.ListInvoices(c.Query("status"), limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	out := make([]fiber.Map, 0, len(rows))
	for _, r := range rows {
		m := invoiceRow(r.InvoiceRow)
		m["tenant_name"] = r.TenantName
		m["covers_active_period"] = r.CoversActivePeriod
		out = append(out, m)
	}
	return c.JSON(fiber.Map{"data": out})
}
