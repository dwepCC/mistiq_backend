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

// BulkUpdatePricesAPI PATCH /api/products/bulk-update-prices — "Actualizar precio" en Productos:
// actualiza precio de venta/compra en masa matcheando cada fila por código de barras.
func (h *ProductHandler) BulkUpdatePricesAPI(c fiber.Ctx) error {
	var body struct {
		BranchID uint `json:"branch_id"`
		Rows     []struct {
			RowNumber     int      `json:"row_number"`
			Code          string   `json:"code"`
			SalePrice     *float64 `json:"sale_price"`
			PurchasePrice *float64 `json:"purchase_price"`
		} `json:"rows"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if _, err := branch.ResolveWriteBranchID(c, body.BranchID); err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	rows := make([]service.BulkPriceUpdateRow, 0, len(body.Rows))
	for _, r := range body.Rows {
		rows = append(rows, service.BulkPriceUpdateRow{
			RowNumber:     r.RowNumber,
			Code:          r.Code,
			SalePrice:     r.SalePrice,
			PurchasePrice: r.PurchasePrice,
		})
	}
	res, err := service.NewProductService(db(c)).BulkUpdatePrices(rows)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": res})
}
