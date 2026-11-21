package handler

import (
	"errors"
	"strconv"
	"strings"

	"tukifac/internal/memberships/service"
	"tukifac/pkg/branch"
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
	var reqBranch uint
	if v := strings.TrimSpace(c.Query("branch_id")); v != "" {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			reqBranch = uint(n)
		}
	}
	// Una sucursal no accesible cae a la activa; sin sucursal resuelta no lista nada (nunca "todas").
	branchID := branch.ResolveReadBranchFilter(c, reqBranch)
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
	overdue, upcoming, err := svc.ReminderCountsInBranch(branch.ResolveReadBranchFilter(c, 0))
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
	if !branch.RecordReadable(c, m.BranchID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": service.ErrMembershipNotFound.Error()})
	}
	return c.JSON(fiber.Map{"data": m})
}

func (h *MembershipHandler) BillingHistoryAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewMembershipService(db(c))
	bm, err := svc.GetByID(uint(id))
	if err != nil {
		if errors.Is(err, service.ErrMembershipNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if !branch.RecordReadable(c, bm.BranchID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": service.ErrMembershipNotFound.Error()})
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
	// La sucursal del cliente se valida (solo la activa o una accesible); nunca se acepta cruda.
	wb, werr := branch.ResolveWriteBranchID(c, body.BranchID)
	if werr != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": werr.Error(), "code": branch.CodeBranchForbidden})
	}
	body.BranchID = wb
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
	if denied, resp := membershipWriteDenied(c, svc, uint(id)); denied {
		return resp
	}
	// Una membresía no cambia de sucursal por edición (evita moverla a una sucursal ajena).
	body.BranchID = nil
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
	if denied, resp := membershipWriteDenied(c, svc, uint(id)); denied {
		return resp
	}
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
	if denied, resp := membershipWriteDenied(c, svc, uint(id)); denied {
		return resp
	}
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
	if denied, resp := membershipWriteDenied(c, svc, uint(id)); denied {
		return resp
	}
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

// membershipWriteDenied guard de escritura por sucursal: inexistente o de una sucursal no visible -> 404;
// visible (admin viendo todas) pero no de la sucursal activa -> 403 BRANCH_FORBIDDEN.
func membershipWriteDenied(c fiber.Ctx, svc *service.MembershipService, id uint) (bool, error) {
	m, err := svc.GetByID(id)
	if err != nil {
		if errors.Is(err, service.ErrMembershipNotFound) {
			return true, c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
		}
		return true, c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if !branch.RecordReadable(c, m.BranchID) {
		return true, c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": service.ErrMembershipNotFound.Error()})
	}
	if !branch.RecordWritable(c, m.BranchID) {
		return true, c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "Solo puede operar membresías de su sucursal activa; cambie de sucursal", "code": branch.CodeBranchForbidden,
		})
	}
	return false, nil
}
