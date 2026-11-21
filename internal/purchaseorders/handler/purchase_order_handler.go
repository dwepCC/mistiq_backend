package handler

import (
	"errors"
	"strconv"
	"strings"
	"time"

	posvc "tukifac/internal/purchaseorders/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/datespe"
	"tukifac/pkg/tax"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type PurchaseOrderHandler struct{}

func NewPurchaseOrderHandler() *PurchaseOrderHandler { return &PurchaseOrderHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

func userID(c fiber.Ctx) uint {
	v, _ := c.Locals("user_id").(uint)
	return v
}

// parseDate interpreta YYYY-MM-DD en hora de Perú y fija el mediodía (misma convención que compras).
func parseDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	loc := datespe.Location()
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return nil
	}
	d := time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
	return &d
}

func today() time.Time {
	loc := datespe.Location()
	n := time.Now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 12, 0, 0, 0, loc)
}

type saveBody struct {
	BranchID         uint              `json:"branch_id"`
	ContactID        *uint             `json:"contact_id"`
	SeriesID         uint              `json:"series_id"`
	IssueDate        string            `json:"issue_date"`
	ExpectedDate     string            `json:"expected_date"`
	Currency         string            `json:"currency"`
	ExchangeRate     *float64          `json:"exchange_rate"`
	PaymentCondition string            `json:"payment_condition"`
	Notes            string            `json:"notes"`
	Items            []posvc.ItemInput `json:"items"`
}

func (b saveBody) input(c fiber.Ctx, branchID uint) posvc.SaveInput {
	issue := today()
	if d := parseDate(b.IssueDate); d != nil {
		issue = *d
	}
	return posvc.SaveInput{
		BranchID:         branchID,
		UserID:           userID(c),
		ContactID:        b.ContactID,
		SeriesID:         b.SeriesID,
		IssueDate:        issue,
		ExpectedDate:     parseDate(b.ExpectedDate),
		Currency:         b.Currency,
		ExchangeRate:     b.ExchangeRate,
		PaymentCondition: b.PaymentCondition,
		Notes:            b.Notes,
		Items:            b.Items,
		TaxConfig:        tax.LoadFromDB(db(c)),
	}
}

func idParam(c fiber.Ctx) (uint, error) {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil || id == 0 {
		return 0, errors.New("ID inválido")
	}
	return uint(id), nil
}

// readable: la orden se lee solo desde una sucursal visible; otra responde "no encontrada".
func readable(c fiber.Ctx, svc *posvc.PurchaseOrderService, id uint) error {
	bid, err := svc.BranchOf(id)
	if err != nil || !branch.RecordReadable(c, bid) {
		return posvc.ErrNotFound
	}
	return nil
}

// writable: además exige operar desde la sucursal activa (no en modo "Todas").
func writable(c fiber.Ctx, svc *posvc.PurchaseOrderService, id uint) (int, error) {
	if err := readable(c, svc, id); err != nil {
		return fiber.StatusNotFound, err
	}
	bid, _ := svc.BranchOf(id)
	if !branch.RecordWritable(c, bid) {
		return fiber.StatusForbidden, errors.New("solo puede operar órdenes de su sucursal activa; cambie de sucursal")
	}
	return 0, nil
}

// GET /api/purchase-orders
func (h *PurchaseOrderHandler) ListAPI(c fiber.Ctx) error {
	svc := posvc.NewPurchaseOrderService(db(c))
	reqBranch, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	p := posvc.ListParams{
		BranchID: branch.ResolveReadBranchFilter(c, uint(reqBranch)),
		Query:    c.Query("q"),
		Status:   strings.TrimSpace(c.Query("status")),
		From:     parseDate(c.Query("from")),
		Limit:    limit,
		Offset:   offset,
	}
	if t := parseDate(c.Query("to")); t != nil {
		end := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, t.Location())
		p.To = &end
	}
	rows, total, err := svc.List(p)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows, "total": total})
}

// GET /api/purchase-orders/:id
func (h *PurchaseOrderHandler) GetAPI(c fiber.Ctx) error {
	id, err := idParam(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	svc := posvc.NewPurchaseOrderService(db(c))
	if err := readable(c, svc, id); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	po, items, err := svc.GetByID(id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": po, "items": items})
}

// POST /api/purchase-orders
func (h *PurchaseOrderHandler) CreateAPI(c fiber.Ctx) error {
	var body saveBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	po, err := posvc.NewPurchaseOrderService(db(c)).Create(body.input(c, branchID))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true, "data": po})
}

// PATCH /api/purchase-orders/:id
func (h *PurchaseOrderHandler) UpdateAPI(c fiber.Ctx) error {
	id, err := idParam(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	svc := posvc.NewPurchaseOrderService(db(c))
	if code, err := writable(c, svc, id); err != nil {
		return c.Status(code).JSON(fiber.Map{"error": err.Error()})
	}
	var body saveBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	bid, _ := svc.BranchOf(id)
	po, err := svc.Update(id, body.input(c, bid))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": po})
}

// POST /api/purchase-orders/:id/send  (draft → sent)
func (h *PurchaseOrderHandler) SendAPI(c fiber.Ctx) error {
	return h.transition(c, func(s *posvc.PurchaseOrderService, id uint) error { return s.MarkSent(id) })
}

// POST /api/purchase-orders/:id/cancel
func (h *PurchaseOrderHandler) CancelAPI(c fiber.Ctx) error {
	return h.transition(c, func(s *posvc.PurchaseOrderService, id uint) error { return s.Cancel(id) })
}

func (h *PurchaseOrderHandler) transition(c fiber.Ctx, fn func(*posvc.PurchaseOrderService, uint) error) error {
	id, err := idParam(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	svc := posvc.NewPurchaseOrderService(db(c))
	if code, err := writable(c, svc, id); err != nil {
		return c.Status(code).JSON(fiber.Map{"error": err.Error()})
	}
	if err := fn(svc, id); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	po, _, _ := svc.GetByID(id)
	return c.JSON(fiber.Map{"success": true, "data": po})
}
