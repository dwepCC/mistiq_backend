package paymentcatalog

import (
	"tukifac/internal/paymentcatalog/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewPaymentCatalogHandler()
	// Métodos de pago: caja o ventas (CxC / registro de ventas).
	payMethods := middleware.RequireAnyModule("cashbank", "sales")

	api.Get("/payment-methods", payMethods, h.ListPaymentMethodsAPI)
	api.Get("/payment-conditions", middleware.RequireModule("sales"), h.ListPaymentConditionsAPI)
	api.Get("/tax-payment-types", middleware.RequireModule("billing"), h.ListTaxPaymentTypesAPI)
}
