package handler

import (
	"tukifac/internal/products/service"
	"tukifac/pkg/branch"

	"github.com/gofiber/fiber/v3"
)

// BulkImportRestaurantAPI POST /api/products/bulk-import/restaurant
func (h *ProductHandler) BulkImportRestaurantAPI(c fiber.Ctx) error {
	var body struct {
		BranchID uint                     `json:"branch_id"`
		Items    []service.BulkImportItem `json:"items"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	uid, _ := c.Locals("user_id").(uint)
	res, err := service.NewProductService(db(c)).BulkImportRestaurant(body.Items, branchID, uid)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": res})
}

// BulkImportCatalogAPI POST /api/products/bulk-import/catalog
func (h *ProductHandler) BulkImportCatalogAPI(c fiber.Ctx) error {
	var body struct {
		BranchID uint                     `json:"branch_id"`
		Items    []service.BulkImportItem `json:"items"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	uid, _ := c.Locals("user_id").(uint)
	res, err := service.NewProductService(db(c)).BulkImportCatalog(body.Items, branchID, uid)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": res})
}
