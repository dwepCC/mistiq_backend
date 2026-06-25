package handler

import (
	"strconv"

	"tukifac/internal/receivables/service"
	"tukifac/pkg/branch"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type ReceivableHandler struct{}

func NewReceivableHandler() *ReceivableHandler { return &ReceivableHandler{} }

func tenantDB(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

func uid(c fiber.Ctx) uint {
	v, _ := c.Locals("user_id").(uint)
	return v
}

// GET /api/receivables
func (h *ReceivableHandler) ListAPI(c fiber.Ctx) error {
	reqBranch, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(reqBranch))
	contactID, _ := strconv.ParseUint(c.Query("contact_id"), 10, 32)
	page, _ := strconv.Atoi(c.Query("page", "1"))
	pageSize, _ := strconv.Atoi(c.Query("page_size", "50"))

	svc := service.NewReceivableService(tenantDB(c))
	rows, total, err := svc.List(service.ListFilter{
		BranchID:  branchID,
		ContactID: uint(contactID),
		Status:    c.Query("status"),
		Search:    c.Query("search"),
		BnStatus:  c.Query("bn_status"),
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows, "total": total})
}

// GET /api/receivables/summary
func (h *ReceivableHandler) SummaryAPI(c fiber.Ctx) error {
	reqBranch, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(reqBranch))
	summary, err := service.NewReceivableService(tenantDB(c)).Summary(branchID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": summary})
}

// GET /api/receivables/statement?contact_id=
func (h *ReceivableHandler) StatementAPI(c fiber.Ctx) error {
	contactID, err := strconv.ParseUint(c.Query("contact_id"), 10, 32)
	if err != nil || contactID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "contact_id requerido"})
	}
	reqBranch, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(reqBranch))
	stmt, err := service.NewReceivableService(tenantDB(c)).Statement(uint(contactID), branchID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": stmt})
}

// POST /api/receivables/:saleId/collect
func (h *ReceivableHandler) CollectAPI(c fiber.Ctx) error {
	saleID, err := strconv.ParseUint(c.Params("saleId"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body service.CollectPaymentInput
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	body.UserID = uid(c)
	if err := service.NewReceivableService(tenantDB(c)).Collect(uint(saleID), body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/receivables/:saleId/confirm-bn
func (h *ReceivableHandler) ConfirmBNAPI(c fiber.Ctx) error {
	saleID, err := strconv.ParseUint(c.Params("saleId"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body service.ConfirmBNInput
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	row, err := service.NewReceivableService(tenantDB(c)).ConfirmBN(uint(saleID), body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": row})
}

// GET /api/receivables/bn-pending
func (h *ReceivableHandler) BnPendingAPI(c fiber.Ctx) error {
	reqBranch, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(reqBranch))
	svc := service.NewReceivableService(tenantDB(c))
	rows, total, err := svc.List(service.ListFilter{
		BranchID: branchID,
		Status:   "bn_pending",
		Page:     1,
		PageSize: 200,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows, "total": total})
}
