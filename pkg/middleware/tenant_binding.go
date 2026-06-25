package middleware

import (
	"strings"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/tenantctx"

	"github.com/gofiber/fiber/v3"
)

// ValidateTenantBinding evita cross-tenant: subdominio/header, JWT y BD del pool deben coincidir.
// Aplicar después de TenantAuthAPI en el grupo /api protegido.
func ValidateTenantBinding() fiber.Handler {
	return func(c fiber.Ctx) error {
		tenant, hasTenant := tenantctx.Tenant(c)
		resolvedSlug := tenantctx.Slug(c)
		resolvedDB := tenantctx.DBName(c)

		claims, hasClaims := c.Locals("tenant_claims").(*TenantClaims)
		if !hasClaims || claims == nil {
			if hasTenant && resolvedSlug != "" && isProtectedTenantAPIPath(c.Path()) {
				return tenantSecurityForbidden(c, "missing_jwt_on_tenant_route")
			}
			return c.Next()
		}

		if resolvedSlug == "" || !hasTenant {
			return tenantSecurityForbidden(c, "missing_resolved_tenant")
		}

		if claims.TenantID == 0 {
			return tenantSecurityForbidden(c, "jwt_missing_tenant_id")
		}
		if strings.TrimSpace(claims.TenantSlug) == "" {
			return tenantSecurityForbidden(c, "jwt_missing_tenant_slug")
		}
		if strings.TrimSpace(claims.TenantDB) == "" {
			return tenantSecurityForbidden(c, "jwt_missing_tenant_db")
		}

		if claims.TenantSlug != resolvedSlug {
			return tenantSecurityForbidden(c, "jwt_slug_mismatch")
		}

		if tenant.DBName != claims.TenantDB || resolvedDB != claims.TenantDB {
			return tenantSecurityForbidden(c, "jwt_db_mismatch")
		}

		if tenant.ID != claims.TenantID {
			return tenantSecurityForbidden(c, "jwt_tenant_id_mismatch")
		}

		if config.AppConfig != nil && config.AppConfig.IsProd() {
			if claims.TenantVersion < MinTenantJWTVersion {
				return tenantSecurityForbidden(c, "jwt_tenant_version_missing")
			}
		}

		// Host resuelto debe coincidir con slug JWT (app móvil en empresa1.tukifac.com).
		if sub, _ := c.Locals("tenant_subdomain_slug").(string); sub != "" && sub != claims.TenantSlug {
			return tenantSecurityForbidden(c, "host_jwt_slug_mismatch")
		}

		// Defensa adicional: user_id del JWT debe existir en la BD de ESTE tenant (anti token reutilizado).
		if claims.UserID > 0 {
			tdb, ok := tenantctx.TenantDB(c)
			if ok && tdb != nil {
				var count int64
				if err := tdb.Model(&database.TenantUser{}).Where("id = ?", claims.UserID).Count(&count).Error; err == nil && count == 0 {
					return tenantSecurityForbidden(c, "jwt_user_not_in_tenant_db")
				}
			}
		}

		c.Locals("tenant_binding_verified", true)
		return c.Next()
	}
}

func isProtectedTenantAPIPath(path string) bool {
	if path == "/api/login" || path == "/api/superadmin/login" {
		return false
	}
	return strings.HasPrefix(path, "/api/")
}
