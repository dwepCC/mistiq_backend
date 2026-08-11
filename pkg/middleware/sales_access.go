package middleware

import (
	"tukifac/pkg/restaurantperm"

	"github.com/gofiber/fiber/v3"
)

func salesTenantPerm(action string) string {
	switch action {
	case "create":
		return "sales.create"
	default:
		return "sales.view"
	}
}

// RequireSalesAccess permite ventas vía permisos tenant (sales.view / sales.create)
// o staff restaurante (cobro o.c, ver caja c.v para consulta).
// Usar después de RequireModule("sales") y LoadRestaurantPermissions().
func RequireSalesAccess(action string) fiber.Handler {
	tenantPerm := salesTenantPerm(action)
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("tenant_claims").(*TenantClaims)
		if !ok || claims == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Sin contexto de autenticación"})
		}
		if tenantHasPermission(claims.Permissions, tenantPerm) {
			return c.Next()
		}
		if claims.AuthMethod == "pin" || claims.EmployeeType != "" {
			if action == "view" {
				if HasRestaurantPerm(c, restaurantperm.OrdersCharge) || HasRestaurantPerm(c, restaurantperm.CashView) {
					return c.Next()
				}
			} else if HasRestaurantPerm(c, restaurantperm.OrdersCharge) {
				return c.Next()
			}
			if claims.RoleName == "Administrador" {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":      "No tienes permiso para ver o registrar ventas",
			"permission": tenantPerm,
		})
	}
}
