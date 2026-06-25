package routes

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"tukifac/config"
	"tukifac/pkg/corspolicy"
	"tukifac/pkg/logger"
	authHandler "tukifac/internal/auth/handler"
	"tukifac/internal/auth"
	"tukifac/internal/billing"
	"tukifac/internal/cashbank"
	"tukifac/internal/company"
	consultaHandler "tukifac/internal/consulta/handler"
	catalogs "tukifac/internal/catalogs"
	"tukifac/internal/contacts"
	"tukifac/internal/dashboard"
	"tukifac/internal/fleet"
	"tukifac/internal/inventory"
	"tukifac/internal/memberships"
	"tukifac/internal/modules"
	"tukifac/internal/paymentcatalog"
	"tukifac/internal/products"
	"tukifac/internal/purchases"
	"tukifac/internal/quotations"
	"tukifac/internal/receivables"
	"tukifac/internal/restaurant"
	"tukifac/internal/sales"
	"tukifac/internal/tenantportal"
	superadmin "tukifac/internal/superadmin"
	"tukifac/internal/fiscal"
	"tukifac/internal/ubigeo"
	"tukifac/internal/users"
	"tukifac/pkg/database"
	"tukifac/pkg/domains"
	"tukifac/pkg/health"
	"tukifac/pkg/middleware"
	"tukifac/pkg/storagepaths"
	"tukifac/pkg/tenantstorage"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
)

func Setup(app *fiber.App) {
	cfg := config.AppConfig
	corsMatcher := corspolicy.NewMatcher(cfg)
	logger.L.Info("domains_configured",
		slog.String("app_env", cfg.AppEnv),
		slog.String("root_domain", cfg.AppDomain),
		slog.String("api_public_url", cfg.APIPublicURL),
		slog.String("frontend_url", cfg.FrontendURL),
		slog.String("central_frontend_url", cfg.CentralFrontendURL),
		slog.Any("reserved_subdomains", cfg.ReservedSubdomains),
		slog.Any("cors_base_hosts", corsMatcher.BaseHosts()),
		slog.Int("cors_exact_origins", corsMatcher.ExactCount()),
	)
	app.Use(middleware.SecurityHeaders())
	app.Use(middleware.RequestID())
	app.Use(middleware.RequestLogger())

	// Health y métricas (sin TenantResolver, rate limit ni auth)
	app.Get("/", health.Liveness)
	app.Get("/health/live", health.Liveness)
	app.Get("/health", health.Readiness)
	app.Get("/metrics", health.Metrics)
	app.Get("/fleet-health", health.FleetHealth)
	app.Get("/api/internal/fleet-health", health.FleetHealth)
	fiscal.RegisterInternalRoutes(app)

	// CORS antes de rate limits: preflight OPTIONS debe recibir Allow-Origin si el origen es válido.
	app.Use(cors.New(cors.Config{
		AllowOriginsFunc: corsMatcher.Allow,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Tenant-Slug"},
		AllowCredentials: true,
		MaxAge:           3600,
	}))

	middleware.ApplyRateLimits(app)

	// Liveness bajo /api para clientes POS (nginx proxy /api; CORS para Capacitor/Tauri).
	app.Get("/api/health/live", health.Liveness)

	// Consulta DNI/RUC (público): valida tenant_ruc en central antes de llamar a apiperu
	consultaH := consultaHandler.NewConsultaHandler()
	app.Post("/api/consulta/dni", consultaH.PublicConsultaDNIAPI)
	app.Post("/api/consulta/ruc", consultaH.PublicConsultaRUCAPI)

	// Archivos subidos por tenant (uploads/tenants/{RUC}/...)
	app.All("/uploads/*", tenantstorage.UploadsHandler)
	app.Get("/storage/*", func(c fiber.Ctx) error {
		p := c.Params("*")
		if p == "" {
			return c.Status(fiber.StatusNotFound).SendString("not found")
		}
		path := storagepaths.FilePath(p)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "archivo no encontrado"})
			}
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		// QR SaaS y assets editables: no cachear agresivamente (reemplazo mantiene misma ruta legacy).
		if strings.HasPrefix(filepath.ToSlash(p), "saas/") {
			c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
			c.Set("Pragma", "no-cache")
		}
		return c.SendFile(path)
	})

	// Middleware global de resolución de tenant por subdominio / header
	app.Use(middleware.TenantResolver())
	app.Use(middleware.TenantDBRelease())

	// ── RUTAS PÚBLICAS (sin middleware de auth) ──
	// Endpoint público: consulta tenant por RUC (módulo restaurante - primera vez)
	app.Get("/api/public/tenant-by-ruc", func(c fiber.Ctx) error {
		ruc := c.Query("ruc")
		if ruc == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Se requiere el RUC"})
		}
		ruc = strings.TrimSpace(ruc)
		ruc = strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, ruc)
		if ruc == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Se requiere el RUC"})
		}
		var tenant database.Tenant
		if err := database.CentralDB.Where("ruc = ? AND status = ?", ruc, "active").First(&tenant).Error; err != nil {
			if err2 := database.CentralDB.
				Where("REPLACE(REPLACE(REPLACE(ruc, '-', ''), ' ', ''), '.', '') = ? AND status = ?", ruc, "active").
				First(&tenant).Error; err2 != nil {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Empresa no encontrada con ese RUC"})
			}
		}
		subdomain := domains.TenantHost(tenant.Slug, config.AppConfig.AppDomain)
		fallbackAPI := ""
		if domains.NormalizeRootDomain(config.AppConfig.AppDomain) == "localhost" {
			fallbackAPI = config.AppConfig.DevTenantAPIOrigin()
		}
		apiURL := domains.ResolveTenantAPIURL(tenant.Slug, config.AppConfig.AppDomain, fallbackAPI)
		return c.JSON(fiber.Map{
			"slug":                 tenant.Slug,
			"tenant_slug":          tenant.Slug,
			"name":                 tenant.Name,
			"subdomain":            subdomain,
			"api_url":              apiURL,
			"tenant_version":       middleware.CurrentTenantJWTVersion(),
			"token_consulta_datos": tenant.TokenConsultaDatos,
		})
	})

	// Super Admin login
	superadmin.RegisterRoutes(app)

	// Tenant login
	auth.RegisterRoutes(app)

	// Restaurante: config y PIN (sin JWT, con tenant)
	restaurant.RegisterPublicRoutes(app.Group("/api"))

	// Utilidades de desarrollo
	if config.AppConfig.IsDev() {
		app.Get("/dev/enter/:slug", func(c fiber.Ctx) error {
			slug := c.Params("slug")
			var tenant database.Tenant
			if err := database.CentralDB.Where("slug = ? AND status = ?", slug, "active").First(&tenant).Error; err != nil {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "tenant no encontrado"})
			}
			c.Cookie(&fiber.Cookie{
				Name:     "dev_tenant",
				Value:    slug,
				Path:     "/",
				MaxAge:   86400,
				HTTPOnly: false,
				SameSite: "Lax",
			})
			return c.JSON(fiber.Map{"slug": slug, "message": "tenant seleccionado"})
		})
		app.Get("/dev/exit", func(c fiber.Ctx) error {
			c.ClearCookie("dev_tenant")
			c.ClearCookie("token")
			return c.JSON(fiber.Map{"message": "tenant limpiado"})
		})
	}
	// ── API TENANT PROTEGIDA ──
	// Se usa un prefijo /api/t/ internamente pero los módulos solo ven su ruta relativa.
	// El middleware TenantAuthAPI se aplica SOLO a las rutas registradas en este grupo,
	// no a las rutas públicas (/api/superadmin/login, /api/login) ya registradas arriba.
	tenantAPI := app.Group("/api", middleware.TenantAuthAPI(), middleware.ValidateTenantBinding(), middleware.SubscriptionGate(), middleware.BranchContextMiddleware())

	sessionH := authHandler.NewSessionHandler()
	tenantAPI.Get("/session/context", sessionH.GetContext)
	tenantAPI.Get("/session/capabilities", sessionH.GetCapabilities)
	tenantAPI.Post("/session/switch-branch", sessionH.SwitchBranch)

	ubigeoTenant := ubigeo.NewTenantHandler()
	tenantAPI.Get("/ubigeo/regiones", ubigeoTenant.RegionesAPI)
	tenantAPI.Get("/ubigeo/provincias", ubigeoTenant.ProvinciasAPI)
	tenantAPI.Get("/ubigeo/distritos", ubigeoTenant.DistritosAPI)

	tenantAPI.Get("/consulta/tipo-cambio", consultaH.TipoCambioAPI)
	catalogs.RegisterRoutes(tenantAPI)

	tenantportal.RegisterRoutes(tenantAPI)
	dashboard.RegisterRoutes(tenantAPI)
	company.RegisterRoutes(tenantAPI)
	users.RegisterRoutes(tenantAPI)
	contacts.RegisterRoutes(tenantAPI)
	products.RegisterRoutes(tenantAPI)
	inventory.RegisterRoutes(tenantAPI)
	sales.RegisterRoutes(tenantAPI)
	quotations.RegisterRoutes(tenantAPI)
	memberships.RegisterRoutes(tenantAPI)
	billing.RegisterRoutes(tenantAPI)
	fleet.RegisterRoutes(tenantAPI)
	purchases.RegisterRoutes(tenantAPI)
	cashbank.RegisterRoutes(tenantAPI)
	paymentcatalog.RegisterRoutes(tenantAPI)
	receivables.RegisterRoutes(tenantAPI)
	restaurant.RegisterRoutes(tenantAPI)
	restaurant.RegisterSalePaymentRoutes(tenantAPI)
	modules.RegisterRoutes(tenantAPI)
	fiscal.RegisterTenantRoutes(tenantAPI)

	// Catch-all
	app.Use(func(c fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Ruta no encontrada"})
	})
}
