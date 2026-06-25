package middleware

import (
	"log/slog"
	"strings"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/tenantctx"
	"tukifac/pkg/tenantstorage"
	"tukifac/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// TenantResolver identifica el tenant activo por request.
// Ver tenant_resolve.go para política host / X-Tenant-Slug / dev.
func TenantResolver() fiber.Handler {
	return func(c fiber.Ctx) error {
		host := c.Hostname()
		headerSlug := c.Get("X-Tenant-Slug")
		cookieSlug := ""
		if config.AppConfig.IsDev() {
			cookieSlug = c.Cookies("dev_tenant")
		}

		subdomainSlug := utils.ExtractSubdomain(host, config.AppConfig.AppDomain)
		c.Locals("tenant_subdomain_slug", subdomainSlug)
		c.Locals("tenant_header_slug", strings.TrimSpace(headerSlug))

		slug, blockReason := resolveTenantSlug(host, headerSlug, cookieSlug, c.Query("tenant_slug"), c.Path(), config.AppConfig)
		if blockReason == "header_subdomain_mismatch" {
			return tenantSecurityForbidden(c, blockReason)
		}
		if blockReason == "central_host_header_fallback" {
			logger.L.Warn("tenant_resolve_central_host_header",
				slog.String("host", host),
				slog.String("header_slug", strings.TrimSpace(headerSlug)),
				slog.String("path", c.Path()),
				slog.String("hint", "use tenant subdomain URL https://{slug}."+config.AppConfig.AppDomain),
			)
		}

		if slug == "" || config.AppConfig.IsReservedSubdomain(slug) {
			return c.Next()
		}

		tenant, err := LookupTenantBySlug(slug)
		if err != nil {
			c.ClearCookie("dev_tenant")
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Empresa no encontrada",
				"slug":  slug,
			})
		}

		tenantDB, err := database.GetTenantDB(tenant.DBName)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString("Error conectando a la base de datos de la empresa")
		}

		tenantctx.Bind(c, tenant, tenantDB)

		if tenant.Status != "active" && tenant.Status != database.TenantStatusBlocked &&
			tenant.Status != database.TenantStatusSuspended && !IsSubscriptionExemptPath(c.Path()) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":  "Cuenta suspendida o inactiva",
				"status": tenant.Status,
			})
		}
		if ruc := tenantstorage.SanitizeRUC(tenant.RUC); ruc != "" {
			c.Locals("tenant_ruc", ruc)
		}
		return c.Next()
	}
}

// RequireTenant verifica que exista un contexto de tenant activo.
func RequireTenant() fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, ok := tenantctx.Tenant(c); !ok {
			return c.Status(fiber.StatusBadRequest).SendString("Acceso no permitido sin contexto de empresa")
		}
		return c.Next()
	}
}
