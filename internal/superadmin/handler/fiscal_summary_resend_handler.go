package handler

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	billingworker "tukifac/internal/billing/worker"
	"tukifac/internal/fiscal/summary"
	"tukifac/pkg/database"
)

// Mínimo entre dos "Reenviar pendientes" del mismo tenant.
const fiscalSummaryResendCooldown = 60 * time.Second

// POST /api/superadmin/fiscal/tenant-summary/:id/resend-pending — reenvía a SUNAT los comprobantes
// electrónicos pendientes o con error del tenant (hasta 100, los más antiguos primero, con más de
// 10 min de creados). Los que SUNAT ya aceptó solo se sincronizan, no se duplican. Exige
// fiscal.retry: es una acción con efecto externo.
func (h *FiscalSummaryHandler) ResendPendingAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id inválido"})
	}
	var tenant database.Tenant
	if err := database.CentralDB.First(&tenant, uint(id)).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant no encontrado"})
	}
	if tenant.Status != "active" {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "El tenant está " + tenant.Status + "; no se reenvían comprobantes de tenants no activos.",
		})
	}

	h.mu.Lock()
	if last, ok := h.lastResend[tenant.ID]; ok && time.Since(last) < fiscalSummaryResendCooldown {
		h.mu.Unlock()
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": "Ya se reenviaron pendientes de este tenant hace instantes; espera un minuto.",
		})
	}
	h.lastResend[tenant.ID] = time.Now()
	h.mu.Unlock()

	var body struct {
		Limit int `json:"limit"`
	}
	_ = c.Bind().Body(&body)

	res, err := billingworker.ResendTenantPending(tenant, body.Limit)
	saUserID, _ := c.Locals("sa_user_id").(uint)
	database.WriteAuditLog(&database.AuditLog{
		UserID: saUserID,
		Action: "fiscal_tenant_resend_pending",
		Entity: "tenant",
		Payload: fmt.Sprintf(
			`{"tenant_id":%d,"found":%d,"queued":%d,"already_accepted":%d,"in_progress":%d,"failed":%d,"ok":%v}`,
			tenant.ID, res.Found, res.Queued, res.AlreadyAccepted, res.InProgress, res.Failed, err == nil),
		IPAddress: c.IP(),
	})
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "No se pudo reenviar: " + err.Error()})
	}
	// Refresca su fila del resumen para que el panel muestre el estado nuevo.
	_ = summary.NewScanner().ScanTenant(tenant, false)
	return c.JSON(res)
}
