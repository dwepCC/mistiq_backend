package receivables

import (
	"tukifac/internal/receivables/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewReceivableHandler()
	mod := middleware.RequireModule("cashbank")
	loadRest := middleware.LoadRestaurantPermissions()

	api.Get("/receivables",
		mod, loadRest, middleware.RequireCashbankAccess("view"), h.ListAPI)
	api.Get("/receivables/summary",
		mod, loadRest, middleware.RequireCashbankAccess("view"), h.SummaryAPI)
	api.Get("/receivables/statement",
		mod, loadRest, middleware.RequireCashbankAccess("view"), h.StatementAPI)
	api.Get("/receivables/bn-pending",
		mod, loadRest, middleware.RequireCashbankAccess("view"), h.BnPendingAPI)
	api.Post("/receivables/:saleId/collect",
		mod, loadRest, middleware.RequireCashbankAccess("movements"), h.CollectAPI)
	api.Post("/receivables/:saleId/confirm-bn",
		mod, loadRest, middleware.RequireCashbankAccess("manage"), h.ConfirmBNAPI)
}
