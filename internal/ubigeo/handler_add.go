package ubigeo

import (
	"errors"
	"strings"

	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
)

// CreateDistritoAPI POST /api/ubigeo/distritos — registra en el catálogo del tenant un distrito que todavía no existe (p. ej. uno creado por
// ley después de la última actualización del catálogo), para poder configurarlo como domicilio fiscal y emitir. Exige el código INEI de 6
// dígitos con departamento y provincia existentes. Si el distrito ya existe, responde 200 con el existente sin modificarlo.
func (h *TenantHandler) CreateDistritoAPI(c fiber.Ctx) error {
	d := db(c)
	if d == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "sin contexto tenant"})
	}
	var body struct {
		ID     string `json:"id"`
		Nombre string `json:"nombre"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "datos inválidos"})
	}
	dist, created, err := database.AddUbigeoDistrito(d, strings.TrimSpace(body.ID), body.Nombre)
	if err != nil {
		if errors.Is(err, database.ErrUbigeoInvalid) {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": "el ubigeo no corresponde a un departamento y provincia existentes: revise el código de 6 dígitos"})
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	status := fiber.StatusOK
	if created {
		status = fiber.StatusCreated
	}
	return c.Status(status).JSON(fiber.Map{"data": dist, "created": created})
}
