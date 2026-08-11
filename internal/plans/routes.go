package plans

import (
	"tukifac/internal/plans/handler"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(saAPI fiber.Router) {
	h := handler.NewPlanHandler()

	saAPI.Get("/saas-modules", h.ListModulesAPI)
	saAPI.Post("/saas-modules", h.CreateModuleAPI)
	saAPI.Put("/saas-modules/:id", h.UpdateModuleAPI)
	saAPI.Patch("/saas-modules/:id/toggle", h.ToggleModuleAPI)
	saAPI.Delete("/saas-modules/:id", h.DeleteModuleAPI)
	saAPI.Get("/plans", h.ListAPI)
	saAPI.Get("/plans/:id", h.GetAPI)
	saAPI.Post("/plans", h.CreateAPI)
	saAPI.Put("/plans/:id", h.UpdateAPI)
	saAPI.Patch("/plans/:id/toggle", h.ToggleAPI)
	saAPI.Delete("/plans/:id", h.DeleteAPI)
}
