package middleware

import (
	"tukifac/pkg/restaurantperm"

	"github.com/gofiber/fiber/v3"
)

func salesTenantPerm(action string) string {
	switch action {
	case "create":
		return "sales.create"
	case "cancel":
		// Anular venta y aplicar devoluciones pendientes de esa anulación: mismo permiso,
		// nunca se validaba en el backend (ver POST /sales/:id/cancel y
		// /sales/pending-refunds[/apply-note] en internal/sales/routes.go) pese a que el
		// catálogo ya lo define y el frontend ya lo usaba para esconder el botón.
		return "sales.cancel"
	default:
		return "sales.view"
	}
}

// RequireSalesAccess permite ventas vía permisos tenant (sales.view / sales.create / sales.cancel)
// o staff restaurante (cobro o.ch, anular o.cx, ver caja c.v para consulta).
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
			switch action {
			case "view":
				if HasRestaurantPerm(c, restaurantperm.OrdersCharge) || HasRestaurantPerm(c, restaurantperm.CashView) {
					return c.Next()
				}
			case "cancel":
				if HasRestaurantPerm(c, restaurantperm.OrdersCancel) {
					return c.Next()
				}
			default:
				if HasRestaurantPerm(c, restaurantperm.OrdersCharge) {
					return c.Next()
				}
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
