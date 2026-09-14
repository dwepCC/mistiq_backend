package handler

import (
	"strconv"

	"tukifac/pkg/database"
	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
)

type SettingsHandler struct{}

func NewSettingsHandler() *SettingsHandler { return &SettingsHandler{} }

func (h *SettingsHandler) GetAPI(c fiber.Ctx) error {
	cfg, err := saas.LoadSettings()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(cfg)
}

// PUT /api/superadmin/saas-settings/operations-key
//
// Autorización: middleware.RequireSuperAdminOnly() en la ruta — Role=="superadmin" exacto,
// SIN excepción y SIN permiso otorgable (rotar la clave que protege destroy-complete no puede
// depender de ningún rol granular, ver Fase 5 etapa 3). ajustes.manage NO alcanza para esto.
func (h *SettingsHandler) SetOperationsKeyAPI(c fiber.Ctx) error {
	var body struct {
		NewOperationsKey     string `json:"new_operations_key"`
		CurrentOperationsKey string `json:"current_operations_key"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if err := saas.SetOperationsKey(body.NewOperationsKey, body.CurrentOperationsKey); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	// Auditoría: nunca se registra el valor de la clave (ni la nueva ni la anterior), solo que se
	// rotó y quién lo hizo.
	saUserID, _ := c.Locals("sa_user_id").(uint)
	database.WriteAuditLog(&database.AuditLog{
		UserID:    saUserID,
		Action:    "operations_key_rotated",
		Entity:    "saas_platform_settings",
		IPAddress: c.IP(),
	})

	return c.JSON(fiber.Map{
		"success":                   true,
		"operations_key_configured": true,
	})
}

func (h *SettingsHandler) PutAPI(c fiber.Ctx) error {
	var body saas.PlatformSettings
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if err := saas.SaveSettings(body); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *SettingsHandler) RunJobsAPI(c fiber.Ctx) error {
	r, n := saas.RunHourlyJobs()
	su, s, oc := saas.RunLimaDailyEvaluation()
	return c.JSON(fiber.Map{
		"success":        true,
		"reminders":      r,
		"notifications":  n,
		"status_updates": su,
		"suspended":      s,
		"overdue_cycles": oc,
	})
}

// POST /api/superadmin/tenants/:id/unblock
func (h *SettingsHandler) UnblockTenantAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.Bind().JSON(&body)
	adminID, _ := c.Locals("sa_user_id").(uint)
	if err := saas.UnblockTenant(uint(id), adminID, body.Reason); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	// saas.UnblockTenant ya registra un SaasSubscriptionEvent; se agrega también en AuditLog por
	// consistencia con el resto de cambios de estado de empresa (Fase 5 etapa 3).
	database.WriteAuditLog(&database.AuditLog{
		TenantID:  uint(id),
		UserID:    adminID,
		Action:    "tenant_unblocked",
		Entity:    "tenant",
		EntityID:  uint(id),
		Payload:   saas.MetaJSON(fiber.Map{"reason": body.Reason}),
		IPAddress: c.IP(),
	})
	return c.JSON(fiber.Map{"success": true, "message": "Tenant desbloqueado; sigue suspendido hasta pago válido"})
}
