package ajustes

import (
	"tukifac/internal/ajustes/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(saAPI fiber.Router) {
	h := handler.NewAjusteHandler()
	saAPI.Get("/ajustes", middleware.RequireSAPermission("ajustes.view"), h.GetAPI)
	saAPI.Put("/ajustes", middleware.RequireSAPermission("ajustes.manage"), h.UpdateAPI)
}
