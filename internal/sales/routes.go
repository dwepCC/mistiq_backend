package sales

import (
	"tukifac/internal/sales/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewSaleHandler()
	mod := middleware.RequireModule("sales")
	loadRest := middleware.LoadRestaurantPermissions()

	api.Get("/sales", mod, loadRest, middleware.RequireSalesAccess("view"), h.ListAPI)
	api.Get("/sales/by-product", mod, loadRest, middleware.RequireSalesAccess("view"), h.ListByProductAPI)
	api.Post("/sales", mod, loadRest, middleware.RequireSalesAccess("create"), h.CreateAPI)
	api.Post("/sales/:id/issue-electronic", mod, middleware.RequireModule("billing"), loadRest, middleware.RequireSalesAccess("create"), h.IssueElectronicFromNotaAPI)
	api.Get("/sales/:id", mod, loadRest, middleware.RequireSalesAccess("view"), h.GetAPI)
}
