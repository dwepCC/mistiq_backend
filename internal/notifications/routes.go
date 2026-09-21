// Package notifications API de lectura del panel interno para TenantNotification — Contrato
// ecommerce v2 §1.7, Fase 6. Sin RequireModule: el badge/campanita debe funcionar para cualquier
// usuario staff autenticado sin importar qué módulos tenga habilitados el tenant (igual que
// /session/*) — el filtrado real de QUÉ ve cada usuario pasa por permisos, no por módulo (ver
// broadcastPermissionByType en internal/notifications/service).
package notifications

import (
	"tukifac/internal/notifications/handler"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewNotificationHandler()
	api.Get("/notifications", h.ListAPI)
	api.Get("/notifications/unread-count", h.UnreadCountAPI)
	api.Post("/notifications/:id/read", h.MarkReadAPI)
	api.Post("/notifications/read-all", h.MarkAllReadAPI)
	api.Get("/notifications/events", handler.SSEAccessTokenMiddleware, h.EventsSSE)
}
