package receivables

import (
	"tukifac/internal/receivables/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes CxC ligado a ventas (módulo sales), sin requerir cashbank en el plan.
func RegisterRoutes(api fiber.Router) {
	h := handler.NewReceivableHandler()
	mod := middleware.RequireModule("sales")
	loadRest := middleware.LoadRestaurantPermissions()

	api.Get("/receivables",
		mod, loadRest, middleware.RequireSalesAccess("view"), h.ListAPI)
	api.Get("/receivables/summary",
		mod, loadRest, middleware.RequireSalesAccess("view"), h.SummaryAPI)
	api.Get("/receivables/statement",
		mod, loadRest, middleware.RequireSalesAccess("view"), h.StatementAPI)
	api.Get("/receivables/bn-pending",
		mod, loadRest, middleware.RequireSalesAccess("view"), h.BnPendingAPI)
	api.Post("/receivables/:saleId/collect",
		mod, loadRest, middleware.RequireSalesAccess("create"), h.CollectAPI)
	api.Post("/receivables/:saleId/confirm-bn",
		mod, loadRest, middleware.RequireSalesAccess("create"), h.ConfirmBNAPI)
}
