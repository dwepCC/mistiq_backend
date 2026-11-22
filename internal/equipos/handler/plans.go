package handler

import "github.com/gofiber/fiber/v3"

// ListPlans GET /api/superadmin/equipos/plans — planes vigentes de Tukifac para el ítem «Plan Tukifac».
func (h *Handler) ListPlans(c fiber.Ctx) error {
	rows, err := svc().ListPlans()
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}
