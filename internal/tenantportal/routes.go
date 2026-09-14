package tenantportal

import (
	"tukifac/internal/tenantportal/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes rutas de suscripción/pagos del tenant (BD central, desacoplado del ERP).
//
// Antes NINGÚN endpoint exigía permiso de rol (solo la autenticación del tenant) — cualquier
// cajero o vendedor podía registrar un pago de suscripción o comprar un paquete de documentos
// SUNAT, con impacto financiero directo. Catálogo nuevo: subscription.{view,manage}.
func RegisterRoutes(api fiber.Router) {
	h := handler.New()
	view := middleware.RequirePermission("subscription.view")
	manage := middleware.RequirePermission("subscription.manage")
	g := api.Group("/subscription")
	g.Get("/summary", view, h.Summary)
	g.Get("/plans", view, h.ListPlans)
	g.Post("/renewal-request", manage, h.SubmitRenewalRequest)
	g.Get("/invoices", view, h.ListInvoices)
	g.Get("/payments", view, h.ListPayments)
	g.Get("/events", view, h.ListEvents)
	g.Post("/payments", manage, h.SubmitPayment)
	g.Get("/document-packages", view, h.ListDocumentPackages)
	g.Post("/document-packages/purchase", manage, h.PurchaseDocumentPackage)
}
