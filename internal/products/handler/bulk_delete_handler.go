package handler

import (
	"errors"
	"strings"

	"tukifac/internal/products/service"
	"tukifac/pkg/branch"

	"github.com/gofiber/fiber/v3"
)

// BulkDeleteRestaurantAPI POST /api/products/bulk-delete/restaurant
func (h *ProductHandler) BulkDeleteRestaurantAPI(c fiber.Ctx) error {
	var body struct {
		ProductIDs []uint `json:"product_ids"`
		Pin        string `json:"pin"`
		Reason     string `json:"reason"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if len(body.ProductIDs) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "se requiere al menos un producto"})
	}
	if strings.TrimSpace(body.Reason) == "" || strings.TrimSpace(body.Pin) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "se requiere motivo y PIN de operaciones"})
	}

	uid, _ := c.Locals("user_id").(uint)
	branchID := branch.ResolveReadBranchFilter(c, 0)

	res, err := service.NewProductService(db(c)).BulkDeleteRestaurant(service.BulkDeleteRestaurantInput{
		ProductIDs: body.ProductIDs,
		Pin:        body.Pin,
		Reason:     body.Reason,
		UserID:     uid,
		BranchID:   branchID,
	})
	if err != nil {
		var pinErr *service.PinVerificationError
		if errors.As(err, &pinErr) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": pinErr.Error()})
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(res)
}
