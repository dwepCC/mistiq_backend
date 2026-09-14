package inventory

import (
	"tukifac/internal/inventory/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes registra las rutas de inventario.
//
// Antes solo exigían el módulo del plan ("inventory"), luego se corrigió a inventory.view/
// inventory.manage (un solo permiso "manage" para crear, confirmar, anular, transferir y
// ajustar). Ahora cada acción tiene su propio permiso — inventory.manage lo sigue concediendo
// todo (implica el resto vía "{modulo}.manage", ver pkg/middleware/tenant_permissions.go), pero un
// tenant puede, por ejemplo, dar "hacer transferencias" sin dar "anular documentos".
func RegisterRoutes(api fiber.Router) {
	h := handler.NewInventoryHandler()
	mod := middleware.RequireModule("inventory")
	loadRest := middleware.LoadRestaurantPermissions()
	view := middleware.RequirePermission("inventory.view")
	// stock-summary/stock/:productId/movements: Tukichef los consulta para el POS (disponibilidad
	// de productos) — con puente de restaurante, a diferencia de documentos/transferencias (sin
	// uso confirmado ahí, quedan solo con permiso ERP).
	viewOrRestaurant := middleware.RequireInventoryViewAccess()
	createDoc := middleware.RequirePermission("inventory.create_document")
	confirmDoc := middleware.RequirePermission("inventory.confirm_document")
	voidDoc := middleware.RequirePermission("inventory.void_document")
	transfer := middleware.RequirePermission("inventory.transfer")
	confirmTransfer := middleware.RequirePermission("inventory.confirm_transfer")
	cancelTransfer := middleware.RequirePermission("inventory.cancel_transfer")
	adjustOrRestaurant := middleware.RequireInventoryAdjustAccess()
	importAdjust := middleware.RequirePermission("inventory.import_adjustment")

	api.Get("/inventory/operation-types", mod, view, h.OperationTypesAPI)
	api.Get("/inventory/documents", mod, view, h.DocumentsListAPI)
	api.Get("/inventory/documents/:id", mod, view, h.DocumentGetAPI)
	api.Post("/inventory/documents", mod, createDoc, h.DocumentCreateAPI)
	api.Put("/inventory/documents/:id", mod, createDoc, h.DocumentUpdateAPI)
	api.Post("/inventory/documents/:id/confirm", mod, confirmDoc, h.DocumentConfirmAPI)
	api.Post("/inventory/documents/:id/void", mod, voidDoc, h.DocumentVoidAPI)
	api.Get("/inventory/stock-summary", mod, loadRest, viewOrRestaurant, h.StockSummaryAPI)
	api.Get("/inventory/stock/:productId", mod, loadRest, viewOrRestaurant, h.StockAPI)
	api.Get("/inventory/movements", mod, loadRest, viewOrRestaurant, h.MovementsAPI)
	api.Get("/inventory/transfers", mod, view, h.TransfersListAPI)
	api.Post("/inventory/transfer", mod, transfer, h.TransferAPI)
	api.Post("/inventory/adjustment", mod, loadRest, adjustOrRestaurant, h.AdjustmentAPI)
	api.Post("/inventory/import-adjustment/preview", mod, importAdjust, h.ImportAdjustmentPreviewAPI)
	api.Post("/inventory/import-adjustment/confirm", mod, importAdjust, h.ImportAdjustmentConfirmAPI)
	api.Post("/inventory/transfers/:id/reverse", mod, cancelTransfer, h.TransferReverseAPI)
	api.Post("/inventory/transfers/:id/confirm", mod, confirmTransfer, h.TransferConfirmAPI)
	api.Post("/inventory/transfers/:id/cancel", mod, cancelTransfer, h.TransferCancelAPI)
}
