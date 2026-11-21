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
	api.Get("/sales/profit-detail", mod, loadRest, middleware.RequireSalesAccess("view"), h.ListProfitDetailAPI)
	api.Post("/sales", mod, loadRest, middleware.RequireSalesAccess("create"), h.CreateAPI)
	// Conversión NV→FE: el frontend ya la esconde sin sales.create (SalesPage.tsx); antes el
	// backend no lo exigía en absoluto (solo el módulo "billing" del plan). Mismo permiso/bridge
	// que crear una venta — emitir el electrónico desde una nota de venta ya hecha es la
	// continuación natural de esa acción.
	api.Post("/sales/:id/issue-electronic", mod, middleware.RequireModule("billing"), loadRest, middleware.RequireSalesAccess("create"), h.IssueElectronicFromNotaAPI)
	// Anular venta: sales.cancel ya existía en el catálogo y el frontend ya lo usaba para
	// esconder el botón (SalesPage.tsx) — el backend nunca lo exigía.
	api.Post("/sales/:id/cancel", mod, loadRest, middleware.RequireSalesAccess("cancel"), h.CancelAPI)
	// Anular localmente un comprobante rechazado por SUNAT (no pasa por NC ni baja: SUNAT nunca lo
	// aceptó). Permiso propio sales.void_rejected; el staff de restaurante usa el mismo puente que
	// anular (orders.cancel) y el rol Administrador.
	api.Post("/sales/:id/void-rejected", mod, loadRest, middleware.RequireSalesAccess("void_rejected"), h.VoidRejectedAPI)
	// Devoluciones pendientes: anulaciones cuyo dinero aún no salió de ninguna caja — misma
	// acción que anular (aplican la devolución de una venta/nota ya anulada), sin permiso propio.
	api.Get("/sales/pending-refunds", mod, loadRest, middleware.RequireSalesAccess("view"), h.PendingRefundsAPI)
	api.Post("/sales/pending-refunds/apply", mod, loadRest, middleware.RequireSalesAccess("cancel"), h.ApplyPendingRefundAPI)
	api.Post("/sales/pending-refunds/apply-note", mod, loadRest, middleware.RequireSalesAccess("cancel"), h.ApplyPendingNoteRefundAPI)
	api.Get("/sales/filter-users", mod, loadRest, middleware.RequireSalesAccess("view"), h.FilterUsersAPI)
	api.Get("/sales/:id", mod, loadRest, middleware.RequireSalesAccess("view"), h.GetAPI)
	api.Post("/sales/:id/email-receipt", mod, loadRest, middleware.RequireSalesAccess("view"), h.EmailReceiptAPI)
}
