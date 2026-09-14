package handler

import (
	"strconv"

	"tukifac/internal/payables/service"
	"tukifac/pkg/branch"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type PayableHandler struct{}

func NewPayableHandler() *PayableHandler { return &PayableHandler{} }

func tenantDB(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

func uid(c fiber.Ctx) uint {
	v, _ := c.Locals("user_id").(uint)
	return v
}

// GET /api/payables
func (h *PayableHandler) ListAPI(c fiber.Ctx) error {
	reqBranch, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(reqBranch))
	contactID, _ := strconv.ParseUint(c.Query("contact_id"), 10, 32)
	page, _ := strconv.Atoi(c.Query("page", "1"))
	pageSize, _ := strconv.Atoi(c.Query("page_size", "50"))

	svc := service.NewPayableService(tenantDB(c))
	rows, total, err := svc.List(service.ListFilter{
		BranchID:  branchID,
		ContactID: uint(contactID),
		Status:    c.Query("status"),
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows, "total": total})
}

// GET /api/payables/summary
func (h *PayableHandler) SummaryAPI(c fiber.Ctx) error {
	reqBranch, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(reqBranch))
	summary, err := service.NewPayableService(tenantDB(c)).Summary(branchID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": summary})
}

// POST /api/payables/:purchaseId/pay
func (h *PayableHandler) PayAPI(c fiber.Ctx) error {
	purchaseID, err := strconv.ParseUint(c.Params("purchaseId"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body service.PayInput
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	body.UserID = uid(c)
	if err := service.NewPayableService(tenantDB(c)).Pay(uint(purchaseID), body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}
