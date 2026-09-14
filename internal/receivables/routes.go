package receivables

import (
	"tukifac/internal/receivables/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes CxC ligado a ventas (módulo sales), sin requerir cashbank en el plan.
//
// Antes reutilizaba sales.view/sales.create: un rol de Ventas quedaba automáticamente habilitado
// para cobrar cuentas por cobrar (una acción financiera), y no había forma de dar "solo cobrar CxC"
// a alguien sin darle además crear/ver ventas. Catálogo propio: receivables.{view,collect,confirm_bn}.
func RegisterRoutes(api fiber.Router) {
	h := handler.NewReceivableHandler()
	mod := middleware.RequireModule("sales")
	loadRest := middleware.LoadRestaurantPermissions()
	view := middleware.RequirePermission("receivables.view")
	collect := middleware.RequirePermission("receivables.collect")
	confirmBN := middleware.RequirePermission("receivables.confirm_bn")

	api.Get("/receivables",
		mod, loadRest, view, h.ListAPI)
	api.Get("/receivables/summary",
		mod, loadRest, view, h.SummaryAPI)
	api.Get("/receivables/statement",
		mod, loadRest, view, h.StatementAPI)
	api.Get("/receivables/bn-pending",
		mod, loadRest, view, h.BnPendingAPI)
	api.Post("/receivables/:saleId/collect",
		mod, loadRest, collect, h.CollectAPI)
	api.Post("/receivables/:saleId/confirm-bn",
		mod, loadRest, confirmBN, h.ConfirmBNAPI)
}
