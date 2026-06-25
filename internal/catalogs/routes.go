package catalogs

import (
	"tukifac/internal/catalogs/handler"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewCatalogHandler()
	api.Get("/catalogs/detraccion/goods", h.DetraccionGoodsAPI)
	api.Get("/catalogs/detraccion/payment-methods", h.DetraccionPaymentMethodsAPI)
}
