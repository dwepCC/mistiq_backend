package middleware

import (
	"tukifac/pkg/restaurantperm"

	"github.com/gofiber/fiber/v3"
)

func contactsTenantPerm(action string) string {
	switch action {
	case "create":
		return "contacts.create"
	case "edit":
		return "contacts.edit"
	default:
		return "contacts.view"
	}
}

// RequireContactsAccess permite acceso a contactos vía permisos tenant (contacts.view/create/edit)
// o staff restaurante (o.c — crear pedido: buscar/dar de alta un cliente es parte de armar la
// venta en el POS de Tukichef, frontend_restaurant_tenant/src/pages/{POS,Mesa,Ventas}Page.tsx).
//
// Sin este puente, RequirePermission("contacts.*") a secas rechaza SIEMPRE a un staff de PIN: su
// JWT nunca llena claims.Permissions (usa pkg/restaurantperm, no TenantPermission) — regresión
// real detectada en producción tras cerrar el hueco de internal/contacts/routes.go: ningún mozo/
// cajero de Tukichef podía ver ni crear clientes para su venta.
//
// "delete" queda fuera a propósito: borrar un contacto no es una operación de POS, sigue exigiendo
// contacts.delete tenant sin puente (igual que antes de este fix).
// Usar después de RequireModule("contacts") y LoadRestaurantPermissions().
func RequireContactsAccess(action string) fiber.Handler {
	tenantPerm := contactsTenantPerm(action)
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("tenant_claims").(*TenantClaims)
		if !ok || claims == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Sin contexto de autenticación"})
		}
		if tenantHasPermission(claims.Permissions, tenantPerm) {
			return c.Next()
		}
		if claims.AuthMethod == "pin" || claims.EmployeeType != "" {
			if HasRestaurantPerm(c, restaurantperm.OrdersCreate) {
				return c.Next()
			}
			if claims.RoleName == "Administrador" {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":      "No tienes permiso para esta acción",
			"permission": tenantPerm,
		})
	}
}
