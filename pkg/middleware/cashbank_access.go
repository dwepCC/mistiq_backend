package middleware

import (
	"tukifac/pkg/restaurantperm"

	"github.com/gofiber/fiber/v3"
)

// cashbankTenantPerm mapea acción → permiso tenant.
func cashbankTenantPerm(action string) string {
	switch action {
	case "open", "adjust_opening":
		// Corregir el monto de apertura es cosa de quien abre y cierra la caja.
		return "cashbank.open"
	case "close":
		return "cashbank.close"
	case "movements":
		return "cashbank.movements"
	case "manage", "delete_session":
		// Borrar una caja es administración, no operación del turno.
		return "cashbank.manage"
	default:
		return "cashbank.view"
	}
}

// restaurantAdminActions acciones que en el restaurante solo hace el administrador.
//
// El personal con caja (mozo, cajero) puede operar su turno, pero corregir el monto de apertura
// o borrar una caja toca el respaldo del dinero contado: eso no se delega al turno.
var restaurantAdminActions = map[string]bool{
	"manage":         true,
	"adjust_opening": true,
	"delete_session": true,
}

// RequireCashbankAccess permite acceso a caja vía permisos tenant o staff restaurante (c.v+).
// Debe usarse después de RequireModule("cashbank") y LoadRestaurantPermissions().
func RequireCashbankAccess(action string) fiber.Handler {
	tenantPerm := cashbankTenantPerm(action)
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("tenant_claims").(*TenantClaims)
		if !ok || claims == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Sin contexto de autenticación"})
		}
		if tenantHasPermission(claims.Permissions, tenantPerm) {
			return c.Next()
		}
		if claims.AuthMethod == "pin" || claims.EmployeeType != "" {
			if restaurantAdminActions[action] {
				if HasRestaurantPerm(c, restaurantperm.SettingsManage) {
					return c.Next()
				}
			} else if HasRestaurantPerm(c, restaurantperm.CashView) {
				return c.Next()
			}
			if claims.RoleName == "Administrador" {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":      "No tienes permiso para operar la caja",
			"permission": tenantPerm,
		})
	}
}
