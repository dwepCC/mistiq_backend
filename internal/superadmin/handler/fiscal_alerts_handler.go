package handler

import (
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v3"

	"tukifac/pkg/database"
	"tukifac/pkg/fiscaladmin"
)

// POST /api/superadmin/fiscal/alerts/:id/acknowledge — "Reconocer": alguien ya vio la alerta;
// deja de contar para el estado del sistema pero sigue visible mientras la condición exista.
func (h *FiscalHandler) AlertAcknowledgeAPI(c fiber.Ctx) error {
	return h.alertAction(c, "acknowledge")
}

// POST /api/superadmin/fiscal/alerts/:id/resolve — "Resolver": se cierra a mano. Si la condición
// sigue ocurriendo, la próxima detección abrirá una alerta nueva.
func (h *FiscalHandler) AlertResolveAPI(c fiber.Ctx) error { return h.alertAction(c, "resolve") }

func (h *FiscalHandler) alertAction(c fiber.Ctx, action string) error {
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id inválido"})
	}

	email, _ := c.Locals("sa_user_email").(string)
	raw, status, err := fiscaladmin.PostJSON(
		fmt.Sprintf("/api/v1/fiscal/alerts/%d/%s", id, action),
		map[string]string{"by": email},
	)
	saUserID, _ := c.Locals("sa_user_id").(uint)
	database.WriteAuditLog(&database.AuditLog{
		UserID:    saUserID,
		Action:    "fiscal_alert_" + action,
		Entity:    "fiscal_alert",
		Payload:   fmt.Sprintf(`{"alert_id":%d,"result_ok":%v}`, id, err == nil),
		IPAddress: c.IP(),
	})
	if err != nil {
		return h.proxyError(c, err, raw, status)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}
