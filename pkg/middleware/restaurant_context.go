package middleware

import (
	"tukifac/internal/restaurant/staff"
	"tukifac/pkg/restaurantperm"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

const restaurantPermsLocal = "restaurant_perm_set"

// LoadRestaurantPermissions carga permisos en Locals desde cache (no en JWT).
func LoadRestaurantPermissions() fiber.Handler {
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("tenant_claims").(*TenantClaims)
		if !ok || claims == nil || claims.UserID == 0 {
			return c.Next()
		}
		tenantDB, _ := c.Locals("tenantDB").(*gorm.DB)
		if tenantDB == nil {
			return c.Next()
		}
		tenantSlug := claims.TenantSlug
		if tenantSlug == "" {
			tenantSlug, _ = c.Locals("tenant_slug").(string)
		}
		svc := staff.New(tenantDB)
		keys, err := svc.ResolvePermissionKeys(tenantSlug, claims.TenantID, claims.UserID, claims.PermVer)
		if err != nil || len(keys) == 0 {
			// Administrador tenant sin fila staff: acceso gestión vía RoleName en handlers dedicados.
			if claims.RoleName == "Administrador" {
				keys = append([]string{}, restaurantperm.AllKeys...)
			} else {
				return c.Next()
			}
		}
		set := make(map[string]struct{}, len(keys))
		for _, k := range keys {
			set[k] = struct{}{}
		}
		c.Locals(restaurantPermsLocal, set)
		return c.Next()
	}
}

// HasRestaurantPerm consulta permisos cargados en Locals.
func HasRestaurantPerm(c fiber.Ctx, perm string) bool {
	if set, ok := c.Locals(restaurantPermsLocal).(map[string]struct{}); ok {
		_, has := set[perm]
		return has
	}
	return false
}

// RequireRestaurantPerm requiere permiso granular staff v2.
func RequireRestaurantPerm(perm string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if HasRestaurantPerm(c, perm) {
			return c.Next()
		}
		et := ""
		if claims, ok := c.Locals("tenant_claims").(*TenantClaims); ok && claims != nil {
			et = claims.EmployeeType
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":         "No tienes permiso para esta acción en el restaurante",
			"employee_type": et,
		})
	}
}

// RequireProductsViewOrRestaurantCatalog lectura de carta: permiso tenant products.view
// o permiso operativo restaurante (mozo, cajero, cocina, etc.). No mezcla roles tenant con staff.
func RequireProductsViewOrRestaurantCatalog() fiber.Handler {
	catalogPerms := []string{
		restaurantperm.OrdersCreate,
		restaurantperm.TablesView,
		restaurantperm.TablesOpen,
		restaurantperm.POSUse,
		restaurantperm.KitchenView,
		restaurantperm.ProductsManage,
	}
	return func(c fiber.Ctx) error {
		if claims, ok := c.Locals("tenant_claims").(*TenantClaims); ok && claims != nil {
			if tenantHasPermission(claims.Permissions, "products.view") {
				return c.Next()
			}
		}
		for _, p := range catalogPerms {
			if HasRestaurantPerm(c, p) {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "No tienes permiso para ver el catálogo de productos",
		})
	}
}

// RequireProductsManageOrTenantWrite permite gestionar catálogo (extras, modificadores, etc.)
// con permiso restaurante g.p o permisos tenant products.create / products.edit / products.delete.
func RequireProductsManageOrTenantWrite() fiber.Handler {
	tenantWrite := []string{"products.create", "products.edit", "products.delete", "products.update"}
	return func(c fiber.Ctx) error {
		if claims, ok := c.Locals("tenant_claims").(*TenantClaims); ok && claims != nil {
			for _, need := range tenantWrite {
				for _, p := range claims.Permissions {
					if p == need {
						return c.Next()
					}
				}
			}
		}
		if HasRestaurantPerm(c, restaurantperm.ProductsManage) {
			return c.Next()
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "No tienes permiso para gestionar el catálogo de productos",
		})
	}
}

// RequireAnyRestaurantPerm permite si tiene al menos uno de los permisos listados.
func RequireAnyRestaurantPerm(perms ...string) fiber.Handler {
	return func(c fiber.Ctx) error {
		for _, p := range perms {
			if HasRestaurantPerm(c, p) {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "No tienes permiso para esta acción en el restaurante",
		})
	}
}

// RequireRestaurantStaff exige perfil operativo (permisos cargados).
func RequireRestaurantStaff() fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, ok := c.Locals(restaurantPermsLocal).(map[string]struct{}); ok {
			return c.Next()
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "No tienes perfil operativo en el restaurante. Contacta al administrador.",
		})
	}
}
