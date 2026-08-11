package middleware

import (
	"github.com/gofiber/fiber/v3"
)

// AuthMethodMasterAccess marca las sesiones abiertas por soporte desde el panel
// central (POST /api/superadmin/tenants/:id/master-access).
const AuthMethodMasterAccess = "master_access"

// IsMasterAccess indica si el request viaja bajo una sesión de soporte.
//
// Exige las dos señales del JWT (auth_method e impersonated) porque se emiten
// juntas al abrir el acceso maestro: si solo llegara una, el token fue alterado
// o proviene de un flujo que aún no las propaga, y en ambos casos no debe pasar.
func IsMasterAccess(c fiber.Ctx) bool {
	claims, ok := c.Locals("tenant_claims").(*TenantClaims)
	if !ok || claims == nil {
		return false
	}
	return claims.Impersonated && claims.AuthMethod == AuthMethodMasterAccess
}

// RequireMasterAccess restringe una ruta a las sesiones de soporte.
//
// Se usa en operaciones que el tenant no debe poder ejecutar por su cuenta
// (p. ej. reemitir un comprobante ya aceptado por SUNAT con otra fecha): quedan
// reservadas a soporte, que entra por acceso maestro y deja rastro auditable.
// Usar siempre después de TenantAuthAPI.
func RequireMasterAccess() fiber.Handler {
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("tenant_claims").(*TenantClaims)
		if !ok || claims == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Sin contexto de autenticación",
			})
		}
		if !claims.Impersonated || claims.AuthMethod != AuthMethodMasterAccess {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Esta acción solo está disponible para soporte mediante acceso maestro",
				"code":  "MASTER_ACCESS_REQUIRED",
			})
		}
		return c.Next()
	}
}
