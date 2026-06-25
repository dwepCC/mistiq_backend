package paymentcatalog

import (
	"tukifac/internal/paymentcatalog/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewPaymentCatalogHandler()
	view := middleware.RequireModule("cashbank")

	api.Get("/payment-methods", view, h.ListPaymentMethodsAPI)
	api.Get("/payment-conditions", view, middleware.RequireModule("sales"), h.ListPaymentConditionsAPI)
	api.Get("/tax-payment-types", view, middleware.RequireModule("billing"), h.ListTaxPaymentTypesAPI)
}
