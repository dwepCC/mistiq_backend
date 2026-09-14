package plans

import (
	"tukifac/internal/plans/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(saAPI fiber.Router) {
	h := handler.NewPlanHandler()

	// Fase 5 (etapa 3, Grupo 5 — planes): el catálogo de módulos (saas-modules) se registra en el
	// mismo dominio "planes" que el catálogo de planes (misma decisión ya vigente para su GET,
	// gateado con planes.view desde la etapa 2) — se le aplican los mismos 4 permisos.
	saAPI.Get("/saas-modules", middleware.RequireSAPermission("planes.view"), h.ListModulesAPI)
	saAPI.Post("/saas-modules", middleware.RequireSAPermission("planes.create"), h.CreateModuleAPI)
	saAPI.Put("/saas-modules/:id", middleware.RequireSAPermission("planes.update"), h.UpdateModuleAPI)
	saAPI.Patch("/saas-modules/:id/toggle", middleware.RequireSAPermission("planes.change_status"), h.ToggleModuleAPI)
	saAPI.Delete("/saas-modules/:id", middleware.RequireSAPermission("planes.destroy"), h.DeleteModuleAPI)
	// Fase 5 (etapa 1 — solo lectura): ver comentario en internal/superadmin/routes.go.
	saAPI.Get("/plans", middleware.RequireSAPermission("planes.view"), h.ListAPI)
	saAPI.Get("/plans/:id", middleware.RequireSAPermission("planes.view"), h.GetAPI)
	saAPI.Post("/plans", middleware.RequireSAPermission("planes.create"), h.CreateAPI)
	saAPI.Put("/plans/:id", middleware.RequireSAPermission("planes.update"), h.UpdateAPI)
	saAPI.Patch("/plans/:id/toggle", middleware.RequireSAPermission("planes.change_status"), h.ToggleAPI)
	// destroy: permiso explícito, NUNCA derivado de view/create/update/change_status ni de un
	// ".manage" (planes no tiene ".manage" en la allowlist — no hay nada de qué implicarlo).
	saAPI.Delete("/plans/:id", middleware.RequireSAPermission("planes.destroy"), h.DeleteAPI)
}
