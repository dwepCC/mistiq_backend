package handler

import (
	"tukifac/internal/contacts/service"

	"github.com/gofiber/fiber/v3"
)

// BulkImportAPI POST /api/contacts/bulk-import — alta masiva de clientes/proveedores.
func (h *ContactHandler) BulkImportAPI(c fiber.Ctx) error {
	var body struct {
		Items []service.BulkImportContactItem `json:"items"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	res, err := service.NewContactService(db(c)).BulkImportContacts(body.Items)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": res})
}
