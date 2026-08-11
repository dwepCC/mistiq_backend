package handler

import (
	detraccionpkg "tukifac/pkg/sunat/detraccion"

	"github.com/gofiber/fiber/v3"
)

// CatalogHandler catálogos SUNAT de solo lectura.
type CatalogHandler struct{}

func NewCatalogHandler() *CatalogHandler {
	return &CatalogHandler{}
}

// GET /api/catalogs/detraccion/goods
func (h *CatalogHandler) DetraccionGoodsAPI(c fiber.Ctx) error {
	cat, err := detraccionpkg.DefaultCatalog()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "catálogo no disponible"})
	}
	excludeTransport := c.Query("exclude_transport", "true") == "true"
	return c.JSON(fiber.Map{"items": cat.ListGoods(excludeTransport)})
}

// GET /api/catalogs/detraccion/payment-methods
func (h *CatalogHandler) DetraccionPaymentMethodsAPI(c fiber.Ctx) error {
	cat, err := detraccionpkg.DefaultCatalog()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "catálogo no disponible"})
	}
	return c.JSON(fiber.Map{"items": cat.ListPaymentMethods()})
}
