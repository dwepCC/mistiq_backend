package inventory

import (
	"tukifac/internal/inventory/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewInventoryHandler()
	api.Get("/inventory/operation-types", middleware.RequireModule("inventory"), h.OperationTypesAPI)
	api.Get("/inventory/documents", middleware.RequireModule("inventory"), h.DocumentsListAPI)
	api.Get("/inventory/documents/:id", middleware.RequireModule("inventory"), h.DocumentGetAPI)
	api.Post("/inventory/documents", middleware.RequireModule("inventory"), h.DocumentCreateAPI)
	api.Put("/inventory/documents/:id", middleware.RequireModule("inventory"), h.DocumentUpdateAPI)
	api.Post("/inventory/documents/:id/confirm", middleware.RequireModule("inventory"), h.DocumentConfirmAPI)
	api.Post("/inventory/documents/:id/void", middleware.RequireModule("inventory"), h.DocumentVoidAPI)
	api.Get("/inventory/stock-summary", middleware.RequireModule("inventory"), h.StockSummaryAPI)
	api.Get("/inventory/stock/:productId", middleware.RequireModule("inventory"), h.StockAPI)
	api.Get("/inventory/movements", middleware.RequireModule("inventory"), h.MovementsAPI)
	api.Get("/inventory/transfers", middleware.RequireModule("inventory"), h.TransfersListAPI)
	api.Post("/inventory/transfer", middleware.RequireModule("inventory"), h.TransferAPI)
	api.Post("/inventory/adjustment", middleware.RequireModule("inventory"), h.AdjustmentAPI)
	api.Post("/inventory/import-adjustment/preview", middleware.RequireModule("inventory"), h.ImportAdjustmentPreviewAPI)
	api.Post("/inventory/import-adjustment/confirm", middleware.RequireModule("inventory"), h.ImportAdjustmentConfirmAPI)
	api.Post("/inventory/transfers/:id/reverse", middleware.RequireModule("inventory"), h.TransferReverseAPI)
	api.Post("/inventory/transfers/:id/confirm", middleware.RequireModule("inventory"), h.TransferConfirmAPI)
	api.Post("/inventory/transfers/:id/cancel", middleware.RequireModule("inventory"), h.TransferCancelAPI)
}
