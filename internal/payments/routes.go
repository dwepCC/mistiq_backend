package payments

import (
	"tukifac/internal/payments/handler"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(saAPI fiber.Router) {
	h := handler.NewPaymentHandler()

	saAPI.Get("/payments", h.ListAPI)
	saAPI.Get("/payments/alerts", h.CollectionAlertsAPI)
	saAPI.Get("/payments/:id", h.GetAPI)
	saAPI.Post("/payments", h.CreateAPI)
	saAPI.Patch("/payments/:id/approve", h.ApproveAPI)
	saAPI.Patch("/payments/:id/reject", h.RejectAPI)
	saAPI.Post("/payments/:id/fiscal-document", h.UploadFiscalDocAPI)
}
