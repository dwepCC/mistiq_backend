package handler

import (
	"tukifac/internal/products/service"
	"tukifac/pkg/branch"

	"github.com/gofiber/fiber/v3"
)

// BulkToggleCatalogAPI PATCH /api/products/bulk-toggle
func (h *ProductHandler) BulkToggleCatalogAPI(c fiber.Ctx) error {
	var body struct {
		ProductIDs []uint `json:"product_ids"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if len(body.ProductIDs) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "se requiere al menos un producto"})
	}

	uid, _ := c.Locals("user_id").(uint)
	branchID := branch.ResolveReadBranchFilter(c, 0)

	res, err := service.NewProductService(db(c)).BulkToggleCatalog(service.BulkToggleCatalogInput{
		ProductIDs: body.ProductIDs,
		UserID:     uid,
		BranchID:   branchID,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(res)
}

// BulkUpdateCatalogAPI PATCH /api/products/bulk-update
func (h *ProductHandler) BulkUpdateCatalogAPI(c fiber.Ctx) error {
	var body struct {
		ProductIDs []uint `json:"product_ids"`
		Updates    struct {
			Active               *bool `json:"active,omitempty"`
			IsRestaurant         *bool `json:"is_restaurant,omitempty"`
			ShowInDigitalCatalog *bool `json:"show_in_digital_catalog,omitempty"`
			ManageStock          *bool `json:"manage_stock,omitempty"`
		} `json:"updates"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if len(body.ProductIDs) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "se requiere al menos un producto"})
	}

	uid, _ := c.Locals("user_id").(uint)
	branchID := branch.ResolveReadBranchFilter(c, 0)

	res, err := service.NewProductService(db(c)).BulkUpdateCatalog(service.BulkUpdateCatalogInput{
		ProductIDs:           body.ProductIDs,
		Active:               body.Updates.Active,
		IsRestaurant:         body.Updates.IsRestaurant,
		ShowInDigitalCatalog: body.Updates.ShowInDigitalCatalog,
		ManageStock:          body.Updates.ManageStock,
		UserID:               uid,
		BranchID:             branchID,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(res)
}
