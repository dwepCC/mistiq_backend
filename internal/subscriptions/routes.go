package subscriptions

import (
	"tukifac/internal/subscriptions/handler"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(saAPI fiber.Router) {
	h := handler.NewSubscriptionHandler()

	saAPI.Get("/subscriptions", h.ListAPI)
	saAPI.Post("/subscriptions", h.CreateAPI)
	saAPI.Patch("/subscriptions/:id/suspend", h.SuspendAPI)
	saAPI.Patch("/subscriptions/:id/reactivate", h.ReactivateAPI)
	saAPI.Patch("/subscriptions/:id/cancel", h.CancelAPI)
	saAPI.Patch("/subscriptions/:id/adjust-validity", h.AdjustValidityAPI)
	saAPI.Get("/tenants/:id/subscription", h.GetByTenantAPI)
	// Cobros emitidos a mano: hasta ahora una factura solo nacía como efecto de crear o
	// renovar una suscripción, sin forma de emitir un cobro puntual a un tenant.
	saAPI.Get("/billing-cycles", h.ListAllInvoicesAPI)
	saAPI.Get("/billing-cycles/preview", h.PreviewInvoiceAPI)
	saAPI.Post("/billing-cycles", h.CreateInvoiceAPI)
	saAPI.Get("/tenants/:id/billing-cycles", h.ListInvoicesAPI)
	saAPI.Patch("/billing-cycles/:id/cancel", h.CancelInvoiceAPI)
	saAPI.Post("/cron/check-expirations", h.CheckExpirationsAPI)
}
