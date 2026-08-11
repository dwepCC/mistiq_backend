package handler

import (
	"errors"
	"strconv"
	"strings"

	"tukifac/internal/memberships/service"
	"tukifac/pkg/database"
	"tukifac/pkg/saas/docusage"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type MembershipHandler struct{}

func NewMembershipHandler() *MembershipHandler { return &MembershipHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

func userID(c fiber.Ctx) uint {
	v, _ := c.Locals("user_id").(uint)
	return v
}

func (h *MembershipHandler) ListAPI(c fiber.Ctx) error {
	svc := service.NewMembershipService(db(c))
	status := strings.TrimSpace(c.Query("status"))
	due := strings.TrimSpace(c.Query("due"))
	q := strings.TrimSpace(c.Query("q"))
	var contactID uint
	if v := strings.TrimSpace(c.Query("contact_id")); v != "" {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			contactID = uint(n)
		}
	}
	var branchID uint
	if v := strings.TrimSpace(c.Query("branch_id")); v != "" {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			branchID = uint(n)
		}
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	rows, total, err := svc.List(service.ListParams{
		Status:    status,
		ContactID: contactID,
		BranchID:  branchID,
		Query:     q,
		Due:       due,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows, "total": total})
}

func (h *MembershipHandler) ReminderCountsAPI(c fiber.Ctx) error {
	svc := service.NewMembershipService(db(c))
	overdue, upcoming, err := svc.ReminderCounts()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"overdue": overdue, "upcoming": upcoming})
}

func (h *MembershipHandler) GetAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewMembershipService(db(c))
	m, err := svc.GetByID(uint(id))
	if err != nil {
		if errors.Is(err, service.ErrMembershipNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": m})
}

func (h *MembershipHandler) BillingHistoryAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewMembershipService(db(c))
	if _, err := svc.GetByID(uint(id)); err != nil {
		if errors.Is(err, service.ErrMembershipNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	rows, err := svc.ListBillingHistory(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *MembershipHandler) CreateAPI(c fiber.Ctx) error {
	var body service.CreateMembershipInput
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	svc := service.NewMembershipService(db(c))
	m, err := svc.Create(body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": m})
}

func (h *MembershipHandler) UpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body service.UpdateMembershipInput
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	svc := service.NewMembershipService(db(c))
	m, err := svc.Update(uint(id), body)
	if err != nil {
		if errors.Is(err, service.ErrMembershipNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": m})
}

func (h *MembershipHandler) SetStatusAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	svc := service.NewMembershipService(db(c))
	if err := svc.SetStatus(uint(id), body.Status); err != nil {
		if errors.Is(err, service.ErrMembershipNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *MembershipHandler) DeleteAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewMembershipService(db(c))
	if err := svc.Delete(uint(id)); err != nil {
		if errors.Is(err, service.ErrMembershipNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *MembershipHandler) GenerateSaleAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body service.GenerateSaleInput
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.SeriesID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "series_id es obligatorio"})
	}
	if len(body.Payments) == 0 && strings.TrimSpace(body.PaymentMethod) == "" {
		body.PaymentMethod = "cash"
	}
	svc := service.NewMembershipService(db(c))
	var centralTenantID uint
	if tenant, ok := c.Locals("tenant").(*database.Tenant); ok && tenant != nil {
		centralTenantID = tenant.ID
	}
	sale, inv, err := svc.GenerateSale(uint(id), userID(c), centralTenantID, body)
	if err != nil {
		st := fiber.StatusBadRequest
		if errors.Is(err, service.ErrBillingNotDue) {
			st = fiber.StatusConflict
		}
		if errors.Is(err, docusage.ErrQuotaExceeded) {
			st = fiber.StatusPaymentRequired
			return c.Status(st).JSON(fiber.Map{
				"error": err.Error(),
				"code":  "DOCUMENT_QUOTA_EXCEEDED",
			})
		}
		return c.Status(st).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"data": fiber.Map{
			"sale":    sale,
			"invoice": inv,
		},
	})
}
