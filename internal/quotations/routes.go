package quotations

import (
	"tukifac/internal/quotations/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewQuotationHandler()
	mod := middleware.RequireModule("sales")

	api.Get("/quotations", mod, middleware.RequireSalesAccess("view"), h.ListAPI)
	api.Get("/quotations/:id", mod, middleware.RequireSalesAccess("view"), h.GetAPI)
	api.Post("/quotations", mod, middleware.RequireSalesAccess("create"), h.CreateAPI)
	api.Patch("/quotations/:id", mod, middleware.RequireSalesAccess("create"), h.UpdateAPI)
	api.Delete("/quotations/:id", mod, middleware.RequireSalesAccess("create"), h.DeleteAPI)
	api.Post("/quotations/:id/convert", mod, middleware.RequireSalesAccess("create"), h.ConvertAPI)
	api.Post("/quotations/:id/email-receipt", mod, middleware.RequireSalesAccess("view"), h.EmailReceiptAPI)
}
