package handler

import (
	"strconv"

	"tukifac/internal/subscriptions/service"
	"tukifac/pkg/database"
	"tukifac/pkg/pagination"
	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
)

// subscriptionStatus lee el estado actual de una suscripción directo de BD — usado solo para
// capturar el "from" en la auditoría antes de cada operación de escritura (el service no expone
// un GetByID propio). Falla en silencio (string vacío) si no se encuentra: la auditoría no debe
// bloquear la operación real.
func subscriptionStatus(id uint) (tenantID uint, status string) {
	var sub database.SaasSubscription
	if err := database.CentralDB.First(&sub, id).Error; err != nil {
		return 0, ""
	}
	return sub.TenantID, sub.Status
}

type SubscriptionHandler struct {
	svc *service.SubscriptionService
}

func NewSubscriptionHandler() *SubscriptionHandler {
	return &SubscriptionHandler{svc: service.NewSubscriptionService()}
}

// GET /api/superadmin/subscriptions?status=&billed_months=&q=&end_date_from=&end_date_to=&page=&per_page=
func (h *SubscriptionHandler) ListAPI(c fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	perPage, _ := strconv.Atoi(c.Query("per_page", "25"))
	page, perPage = pagination.Normalize(page, perPage)
	billedMonths, _ := strconv.Atoi(c.Query("billed_months"))

	subs, total, err := h.svc.List(service.SubscriptionListParams{
		Status:       c.Query("status"),
		BilledMonths: billedMonths,
		Query:        c.Query("q"),
		EndDateFrom:  c.Query("end_date_from"),
		EndDateTo:    c.Query("end_date_to"),
		Page:         page,
		PerPage:      perPage,
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

	saUserID, _ := c.Locals("sa_user_id").(uint)
	database.WriteAuditLog(&database.AuditLog{
		TenantID:  sub.TenantID,
		UserID:    saUserID,
		Action:    "subscription_created",
		Entity:    "saas_subscription",
		EntityID:  sub.ID,
		Payload:   saas.MetaJSON(fiber.Map{"to": sub.Status, "plan_id": sub.PlanID}),
		IPAddress: c.IP(),
	})

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

	tenantID, oldStatus := subscriptionStatus(uint(id))
	if err := h.svc.Suspend(uint(id), body.Reason); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	saUserID, _ := c.Locals("sa_user_id").(uint)
	database.WriteAuditLog(&database.AuditLog{
		TenantID:  tenantID,
		UserID:    saUserID,
		Action:    "subscription_suspended",
		Entity:    "saas_subscription",
		EntityID:  uint(id),
		Payload:   saas.MetaJSON(fiber.Map{"from": oldStatus, "to": database.SaasSubSuspended, "reason": body.Reason}),
		IPAddress: c.IP(),
	})

	return c.JSON(fiber.Map{"success": true})
}

// PATCH /api/superadmin/subscriptions/:id/cancel — anula la suscripción (alta no concretada
// o baja del cliente). Conserva los datos del tenant.
//
// Autorización: suscripciones.change_status (Fase 5 etapa 3) — ya NO es superadmin-only
// hardcodeado, mismo criterio que suspend/reactivate. El permiso ya estaba anticipado para el rol
// Admin desde la Fase 1.
func (h *SubscriptionHandler) CancelAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Reason string `json:"reason"`
	}
	c.Bind().JSON(&body)

	tenantID, oldStatus := subscriptionStatus(uint(id))
	if err := h.svc.Cancel(uint(id), body.Reason); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	saUserID, _ := c.Locals("sa_user_id").(uint)
	database.WriteAuditLog(&database.AuditLog{
		TenantID:  tenantID,
		UserID:    saUserID,
		Action:    "subscription_cancelled",
		Entity:    "saas_subscription",
		EntityID:  uint(id),
		Payload:   saas.MetaJSON(fiber.Map{"from": oldStatus, "to": database.SaasSubCancelled, "reason": body.Reason}),
		IPAddress: c.IP(),
	})

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

	tenantID, oldStatus := subscriptionStatus(uint(id))
	if err := h.svc.Reactivate(uint(id), body.ExtraMonths); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	saUserID, _ := c.Locals("sa_user_id").(uint)
	database.WriteAuditLog(&database.AuditLog{
		TenantID:  tenantID,
		UserID:    saUserID,
		Action:    "subscription_reactivated",
		Entity:    "saas_subscription",
		EntityID:  uint(id),
		Payload:   saas.MetaJSON(fiber.Map{"from": oldStatus, "to": database.SaasSubActive, "extra_months": body.ExtraMonths}),
		IPAddress: c.IP(),
	})

	return c.JSON(fiber.Map{"success": true})
}

// PATCH /api/superadmin/subscriptions/:id/adjust-validity
//
// Autorización: suscripciones.update (Fase 5 etapa 3) — ya NO es superadmin-only hardcodeado.
// Análisis: modificar manualmente la vigencia es sensible (impacto financiero/de acceso), pero no
// es equivalente a un bypass de sistema como destroy-complete/operations-key — es una acción de
// atención al cliente corregible con otra llamada, no irreversible ni destructiva de datos, y
// suscripciones.update ya estaba anticipado para el rol Admin desde el catálogo de la Fase 1
// (mismo razonamiento ya aprobado para empresas.master_access en el Grupo 1). La invalidación de
// sesión no aplica aquí: esta operación no modifica rol/permisos/Active/TokenVersion de ningún
// SuperAdminUser, solo la vigencia de una suscripción — no hay nada que invalidar.
func (h *SubscriptionHandler) AdjustValidityAPI(c fiber.Ctx) error {
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

// POST /api/superadmin/cron/check-expirations
func (h *SubscriptionHandler) CheckExpirationsAPI(c fiber.Ctx) error {
	r, u, s := saas.RunDailyJobs()
	return c.JSON(fiber.Map{
		"reminders": r, "status_updates": u, "suspended": s,
		"message": "verificación completada",
	})
}
