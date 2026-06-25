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
	// Conversión NV→FE: operación operativa del restaurante (sin permiso sales.create).
	api.Post("/sales/:id/issue-electronic", mod, middleware.RequireModule("billing"), loadRest, h.IssueElectronicFromNotaAPI)
	api.Post("/sales/:id/cancel", mod, loadRest, h.CancelAPI)
	api.Get("/sales/:id", mod, loadRest, middleware.RequireSalesAccess("view"), h.GetAPI)
	api.Post("/sales/:id/email-receipt", mod, loadRest, middleware.RequireSalesAccess("view"), h.EmailReceiptAPI)
}
