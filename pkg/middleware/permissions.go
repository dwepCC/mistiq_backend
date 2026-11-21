package middleware

import (
	"github.com/gofiber/fiber/v3"
)

// RequirePermission verifica que el usuario tenga el permiso indicado (formato "module.action"),
// incluyendo las implicaciones de tenantHasPermission (p. ej. "{modulo}.manage" concede todas las
// acciones de ese módulo). Antes esta función solo hacía match exacto contra claims.Permissions,
// distinto del criterio que ya usaban RequireSalesAccess/RequireCashbankAccess (tenantHasPermission)
// — un mismo permiso "funcionaba" en unos módulos y no en otros según qué helper protegía la ruta.
// Unificado para que asignar/quitar un permiso en Roles tenga el mismo efecto en todos los módulos.
// Usa los permisos cargados en el JWT (c.Locals("tenant_claims")). Debe usarse después de TenantAuthAPI.
func RequirePermission(permission string) fiber.Handler {
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("tenant_claims").(*TenantClaims)
		if !ok || claims == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Sin contexto de autenticación",
			})
		}
		if claims.Permissions == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Sesión sin permisos. Inicia sesión de nuevo.",
			})
		}
		if tenantHasPermission(claims.Permissions, permission) {
			return c.Next()
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":      "No tienes permiso para esta acción",
			"permission": permission,
		})
	}
}

// HasPermission expone tenantHasPermission (con sus mismas implicancias, ej. "{modulo}.manage")
// para código fuera de este paquete que necesita el booleano en vez de bloquear la ruta completa
// — ej. un service que solo exime UNA validación interna según el permiso del usuario, sin
// rechazar el request entero (ver sales.override_price / validateAuthorizedPrices).
func HasPermission(claims *TenantClaims, permission string) bool {
	if claims == nil {
		return false
	}
	return tenantHasPermission(claims.Permissions, permission)
}

// RequireAnyPermission permite el acceso si el usuario tiene AL MENOS UNO de los permisos dados
// (con las mismas implicancias de RequirePermission). Para endpoints que sirven más de una acción
// de negocio a la vez (ej. un mismo POST que puede crear una nota de crédito o una de débito según
// el body) y no tiene sentido exigir los dos permisos a la vez.
func RequireAnyPermission(permissions ...string) fiber.Handler {
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("tenant_claims").(*TenantClaims)
		if !ok || claims == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Sin contexto de autenticación",
			})
		}
		if claims.Permissions == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Sesión sin permisos. Inicia sesión de nuevo.",
			})
		}
		for _, p := range permissions {
			if tenantHasPermission(claims.Permissions, p) {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":       "No tienes permiso para esta acción",
			"permissions": permissions,
		})
	}
}
