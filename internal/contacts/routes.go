package contacts

import (
	"tukifac/internal/contacts/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewContactHandler()
	api.Get("/contacts/default", middleware.RequireModule("contacts"), h.DefaultClientAPI)
	api.Get("/contacts", middleware.RequireModule("contacts"), h.SearchAPI)
	api.Get("/contacts/:id", middleware.RequireModule("contacts"), h.GetAPI)
	api.Post("/contacts", middleware.RequireModule("contacts"), h.CreateAPI)
	// Import masivo antes de /contacts/:id para que «bulk-import» no se lea como un id.
	api.Post("/contacts/bulk-import", middleware.RequireModule("contacts"), h.BulkImportAPI)
	api.Put("/contacts/:id", middleware.RequireModule("contacts"), h.UpdateAPI)
	api.Post("/contacts/:id/photo", middleware.RequireModule("contacts"), h.UploadPhotoAPI)
	api.Delete("/contacts/:id", middleware.RequireModule("contacts"), h.DeleteAPI)
	api.Patch("/contacts/:id/toggle", middleware.RequireModule("contacts"), h.ToggleAPI)
}
