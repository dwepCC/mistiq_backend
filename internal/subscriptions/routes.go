package subscriptions

import (
	"tukifac/internal/subscriptions/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(saAPI fiber.Router) {
	h := handler.NewSubscriptionHandler()

	// Fase 5 (etapa 1 — solo lectura): ver comentario en internal/superadmin/routes.go.
	saAPI.Get("/subscriptions", middleware.RequireSAPermission("suscripciones.view"), h.ListAPI)
	saAPI.Post("/subscriptions", middleware.RequireSAPermission("suscripciones.create"), h.CreateAPI)
	saAPI.Patch("/subscriptions/:id/suspend", middleware.RequireSAPermission("suscripciones.change_status"), h.SuspendAPI)
	saAPI.Patch("/subscriptions/:id/reactivate", middleware.RequireSAPermission("suscripciones.change_status"), h.ReactivateAPI)
	saAPI.Patch("/subscriptions/:id/cancel", middleware.RequireSAPermission("suscripciones.change_status"), h.CancelAPI)
	saAPI.Patch("/subscriptions/:id/adjust-validity", middleware.RequireSAPermission("suscripciones.update"), h.AdjustValidityAPI)
	saAPI.Get("/tenants/:id/subscription", middleware.RequireSAPermission("suscripciones.view"), h.GetByTenantAPI)
	// Cobros emitidos a mano: hasta ahora una factura solo nacía como efecto de crear o
	// renovar una suscripción, sin forma de emitir un cobro puntual a un tenant.
	saAPI.Get("/billing-cycles", middleware.RequireSAPermission("suscripciones.view"), h.ListAllInvoicesAPI)
	saAPI.Get("/billing-cycles/preview", middleware.RequireSAPermission("suscripciones.view"), h.PreviewInvoiceAPI)
	saAPI.Post("/billing-cycles", middleware.RequireSAPermission("suscripciones.create"), h.CreateInvoiceAPI)
	saAPI.Get("/tenants/:id/billing-cycles", middleware.RequireSAPermission("suscripciones.view"), h.ListInvoicesAPI)
	saAPI.Patch("/billing-cycles/:id/cancel", middleware.RequireSAPermission("suscripciones.change_status"), h.CancelInvoiceAPI)
	// check-expirations: analizado en detalle (Fase 5 etapa 3, Grupo 3) — RunDailyJobs() recorre
	// TODAS las suscripciones no canceladas/expiradas de la plataforma (recordatorios, expira
	// provisionales, evalúa mora, puede suspender tenants) — es una operación de alcance de FLOTA,
	// misma categoría que /cron/saas-jobs y /migrations/bulk/* ya diferidas. Deliberadamente NO se
	// le asignó suscripciones.update (sería incorrecto: una sola suscripción vs. toda la flota) ni
	// se creó un permiso nuevo — queda pendiente para el tratamiento de operaciones masivas.
	saAPI.Post("/cron/check-expirations", h.CheckExpirationsAPI)
}
