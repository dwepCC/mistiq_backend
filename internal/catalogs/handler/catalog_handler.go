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
//
// only_transport=true: para el combo de 1004, que exige exclusivamente el código 027 (nunca
// mezclado con el resto). No amplía ListGoods — filtra acá mismo sobre el resultado sin excluir
// transporte, para no tocar la firma que ya usa el resto de llamadores.
func (h *CatalogHandler) DetraccionGoodsAPI(c fiber.Ctx) error {
	cat, err := detraccionpkg.DefaultCatalog()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "catálogo no disponible"})
	}
	if c.Query("only_transport", "false") == "true" {
		items := make([]detraccionpkg.GoodEntry, 0, 1)
		for _, g := range cat.ListGoods(false) {
			if g.TransportCargo {
				items = append(items, g)
			}
		}
		return c.JSON(fiber.Map{"items": items})
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
