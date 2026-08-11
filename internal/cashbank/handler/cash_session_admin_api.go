package handler

import (
	"strconv"

	"tukifac/internal/cashbank/service"

	"github.com/gofiber/fiber/v3"
)

// PATCH /api/cashbank/sessions/:id/opening-balance — corregir el monto de apertura.
func (h *CashBankHandler) UpdateOpeningBalanceAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		OpeningBalance float64 `json:"opening_balance"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}

	svc := service.NewCashBankService(db(c))
	session, err := svc.GetSessionByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	// Un cajero solo corrige la suya; quien administra la caja, cualquiera de la sucursal.
	if !canAccessCashSession(c, session) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "No puede modificar esta sesión de caja"})
	}

	updated, err := svc.UpdateOpeningBalance(uint(id), body.OpeningBalance)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": updated})
}

// DELETE /api/cashbank/sessions/:id — borra una caja sin movimientos ni ventas.
func (h *CashBankHandler) DeleteSessionAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}

	svc := service.NewCashBankService(db(c))
	session, err := svc.GetSessionByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	if !canAccessCashSession(c, session) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "No puede eliminar esta sesión de caja"})
	}

	if err := svc.DeleteEmptySession(uint(id)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}
