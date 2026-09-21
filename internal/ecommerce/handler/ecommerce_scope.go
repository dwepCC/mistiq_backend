package handler

import (
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// hasEcommercePermission mismo patrón que internal/cashbank/handler/cashbank_scope.go: lee
// tenant_claims directamente y compara contra el permiso exacto más "ecommerce.manage" (que ya
// implica todas las acciones del módulo vía la regla genérica {modulo}.manage — ver
// pkg/middleware/tenant_permissions.go). Se usa para validar el permiso ESPECÍFICO que exige cada
// transición de estado (Contrato v2 §5/§6.2), algo que RequirePermission por sí solo no puede
// hacer porque el permiso requerido depende del body de la petición, no solo de la ruta.
func hasEcommercePermission(c fiber.Ctx, permission string) bool {
	claims, ok := c.Locals("tenant_claims").(*middleware.TenantClaims)
	if !ok || claims == nil {
		return false
	}
	for _, p := range claims.Permissions {
		if p == permission || p == "ecommerce.manage" {
			return true
		}
	}
	return false
}
