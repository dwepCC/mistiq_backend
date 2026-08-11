package handler

import (
	"strconv"
	"strings"

	"tukifac/internal/subscriptions/service"
	"tukifac/pkg/pagination"
	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
)

type SubscriptionHandler struct {
	svc *service.SubscriptionService
}

func NewSubscriptionHandler() *SubscriptionHandler {
	return &SubscriptionHandler{svc: service.NewSubscriptionService()}
}

// GET /api/superadmin/subscriptions?status=&q=&page=&per_page=
func (h *SubscriptionHandler) ListAPI(c fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	perPage, _ := strconv.Atoi(c.Query("per_page", "25"))
	page, perPage = pagination.Normalize(page, perPage)

	subs, total, err := h.svc.List(service.SubscriptionListParams{
		Status:  c.Query("status"),
		Query:   c.Query("q"),
		Page:    page,
		PerPage: perPage,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"data":        subs,
		"page":        page,
		"per_page":    perPage,
		"total":       total,
		"total_pages": pagination.TotalPages(total, perPage),
	})
}

// GET /api/superadmin/tenants/:id/subscription
func (h *SubscriptionHandler) GetByTenantAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	sub, err := h.svc.GetByTenant(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(sub)
}

// POST /api/superadmin/subscriptions
func (h *SubscriptionHandler) CreateAPI(c fiber.Ctx) error {
	var input service.CreateSubscriptionInput
	if err := c.Bind().JSON(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	sub, err := h.svc.Create(input)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	// El cobro del alta va en la respuesta para poder registrar el pago en el mismo paso,
	// sin una consulta extra para averiguar qué ciclo se acaba de emitir.
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"success":       true,
		"data":          sub,
		"billing_cycle": h.svc.PendingCycleForSubscription(sub.ID),
	})
}

// PATCH /api/superadmin/subscriptions/:id/suspend
func (h *SubscriptionHandler) SuspendAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Reason string `json:"reason"`
	}
	c.Bind().JSON(&body)
	if err := h.svc.Suspend(uint(id), body.Reason); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// PATCH /api/superadmin/subscriptions/:id/cancel — anula la suscripción (alta no concretada
// o baja del cliente). Conserva los datos del tenant.
func (h *SubscriptionHandler) CancelAPI(c fiber.Ctx) error {
	if err := requireSuperAdminRole(c); err != nil {
		return err
	}
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Reason string `json:"reason"`
	}
	c.Bind().JSON(&body)
	if err := h.svc.Cancel(uint(id), body.Reason); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// PATCH /api/superadmin/subscriptions/:id/reactivate
func (h *SubscriptionHandler) ReactivateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		ExtraMonths int `json:"extra_months"`
	}
	c.Bind().JSON(&body)
	if err := h.svc.Reactivate(uint(id), body.ExtraMonths); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// PATCH /api/superadmin/subscriptions/:id/adjust-validity
func (h *SubscriptionHandler) AdjustValidityAPI(c fiber.Ctx) error {
	if err := requireSuperAdminRole(c); err != nil {
		return err
	}
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body service.AdjustValidityInput
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	saUserID, _ := c.Locals("sa_user_id").(uint)
	sub, err := h.svc.AdjustValidity(uint(id), saUserID, c.IP(), body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": sub})
}

func requireSuperAdminRole(c fiber.Ctx) error {
	role, _ := c.Locals("sa_user_role").(string)
	if strings.TrimSpace(role) != "superadmin" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "No autorizado"})
	}
	return nil
}

// POST /api/superadmin/cron/check-expirations
func (h *SubscriptionHandler) CheckExpirationsAPI(c fiber.Ctx) error {
	r, u, s := saas.RunDailyJobs()
	return c.JSON(fiber.Map{
		"reminders": r, "status_updates": u, "suspended": s,
		"message": "verificación completada",
	})
}
