package purchaseorders

import (
	"tukifac/internal/purchaseorders/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes: órdenes de compra. Cuelgan del módulo "purchases" (los planes que ya incluyen
// compras las obtienen sin cambios) y reutilizan sus permisos: ver/crear/eliminar.
func RegisterRoutes(api fiber.Router) {
	h := handler.NewPurchaseOrderHandler()
	mod := middleware.RequireModule("purchases")
	view := middleware.RequirePermission("purchases.view")
	create := middleware.RequirePermission("purchases.create")
	del := middleware.RequirePermission("purchases.delete")

	api.Get("/purchase-orders", mod, view, h.ListAPI)
	api.Get("/purchase-orders/:id", mod, view, h.GetAPI)
	api.Post("/purchase-orders", mod, create, h.CreateAPI)
	api.Patch("/purchase-orders/:id", mod, create, h.UpdateAPI)
	api.Post("/purchase-orders/:id/send", mod, create, h.SendAPI)
	api.Post("/purchase-orders/:id/cancel", mod, del, h.CancelAPI)
}
