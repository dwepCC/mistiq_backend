package handler

import (
	"strings"

	"tukifac/internal/contacts/service"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// Clientes y proveedores son la misma tabla pero con permisos propios (customers.* / suppliers.*).
// RequireContactsAccess deja el alcance de la acción en la petición; aquí se aplica por contacto.

func contactTypeOf(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "supplier", "proveedor":
		return "supplier"
	case "both", "ambos":
		return "both"
	default:
		return "customer"
	}
}

// contactAccessError responde 404 (no existe) o 403 (tipo fuera del alcance del rol).
func contactAccessError(c fiber.Ctx, status int) error {
	if status == fiber.StatusNotFound {
		return c.Status(status).JSON(fiber.Map{"error": "No encontrado"})
	}
	return c.Status(status).JSON(fiber.Map{"error": "No tienes permiso para esta acción sobre este tipo de contacto"})
}

// scopedContactStatus carga el contacto y comprueba que el alcance de la acción lo cubra.
// Devuelve 0 si puede continuar; si no, el status HTTP a responder con contactAccessError.
func scopedContactStatus(c fiber.Ctx, svc *service.ContactService, id uint, needAll bool) int {
	contact, err := svc.GetByID(id)
	if err != nil {
		return fiber.StatusNotFound
	}
	if !middleware.ContactsScopeFrom(c).Allows(contactTypeOf(contact.Type), needAll) {
		return fiber.StatusForbidden
	}
	return 0
}
