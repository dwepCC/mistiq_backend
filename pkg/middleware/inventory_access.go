package middleware

import (
	"tukifac/pkg/restaurantperm"

	"github.com/gofiber/fiber/v3"
)

// requireInventoryPermOrRestaurant permisos comunes a las dos funciones de abajo.
func requireInventoryPermOrRestaurant(tenantPerm string, restaurantPerms []string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if claims, ok := c.Locals("tenant_claims").(*TenantClaims); ok && claims != nil {
			if tenantHasPermission(claims.Permissions, tenantPerm) {
				return c.Next()
			}
		}
		for _, p := range restaurantPerms {
			if HasRestaurantPerm(c, p) {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":      "No tienes permiso para esta acción",
			"permission": tenantPerm,
		})
	}
}

// RequireInventoryViewAccess permite ver stock/kardex vía inventory.view tenant o el mismo set de
// permisos de restaurante que ya usa el catálogo de productos (ver
// RequireProductsViewOrRestaurantCatalog en restaurant_context.go) — Tukichef consulta
// stock-summary/movements para mostrar disponibilidad en el POS
// (frontend_restaurant_tenant/src/services/{inventory,products}.service.ts). Regresión real
// detectada en producción: sin este puente, ningún staff de PIN podía ver stock.
func RequireInventoryViewAccess() fiber.Handler {
	return requireInventoryPermOrRestaurant("inventory.view", []string{
		restaurantperm.OrdersCreate,
		restaurantperm.TablesView,
		restaurantperm.TablesOpen,
		restaurantperm.POSUse,
		restaurantperm.KitchenView,
		restaurantperm.ProductsManage,
	})
}

// RequireInventoryAdjustAccess permite ajustar stock vía inventory.adjust tenant o
// restaurantperm.ProductsManage (mismo bridge que ya usa RequireProductsManageOrTenantWrite).
func RequireInventoryAdjustAccess() fiber.Handler {
	return requireInventoryPermOrRestaurant("inventory.adjust", []string{restaurantperm.ProductsManage})
}
