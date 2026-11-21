package middleware

import (
	"tukifac/pkg/database"
	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
)

// SubscriptionGate restringe módulos operativos si la suscripción no permite operar.
func SubscriptionGate() fiber.Handler {
	return func(c fiber.Ctx) error {
		if IsSubscriptionExemptPath(c.Path()) {
			return c.Next()
		}
		tenant, ok := c.Locals("tenant").(*database.Tenant)
		if !ok || tenant == nil {
			return c.Next()
		}
		view, err := saas.GetTenantView(tenant.ID)
		if err != nil {
			return c.Next()
		}
		c.Locals("subscription_view", &view)
		if view.CanOperate {
			return c.Next()
		}
		// Suspendida/vencida por falta de pago: solo se permite CONSULTAR los reportes.
		if view.CanViewReports && IsReportsReadPath(c.Method(), c.Path()) {
			return c.Next()
		}
		code := "SUBSCRIPTION_REQUIRED"
		if view.IsBlocked {
			code = "TENANT_BLOCKED"
		}
		return c.Status(fiber.StatusPaymentRequired).JSON(fiber.Map{
			"error":            "Acceso operativo restringido",
			"code":             code,
			"subscription":     view,
			"portal_url":       view.PortalURL,
			"pending_amount":   view.PendingAmount,
			"reconnection_fee": view.ReconnectionFee,
			"can_submit_payment": view.CanSubmitPayment,
			"support_message":  view.SupportMessage,
		})
	}
}
