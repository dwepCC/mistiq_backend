package routes

import (
	"log/slog"
	"strings"

	"tukifac/config"
	"tukifac/pkg/corspolicy"
	"tukifac/pkg/logger"
	"tukifac/internal/auth"
	"tukifac/internal/billing"
	"tukifac/internal/cashbank"
	"tukifac/internal/company"
	consultaHandler "tukifac/internal/consulta/handler"
	"tukifac/internal/contacts"
	"tukifac/internal/dashboard"
	"tukifac/internal/inventory"
	"tukifac/internal/memberships"
	"tukifac/internal/modules"
	"tukifac/internal/products"
	"tukifac/internal/purchases"
	"tukifac/internal/restaurant"
	"tukifac/internal/sales"
	superadmin "tukifac/internal/superadmin"
	"tukifac/internal/ubigeo"
	"tukifac/internal/users"
	"tukifac/pkg/database"
	"tukifac/pkg/health"
	"tukifac/pkg/middleware"
	"tukifac/pkg/tenantstorage"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
)

func Setup(app *fiber.App) {
	cfg := config.AppConfig
	corsMatcher := corspolicy.NewMatcher(cfg)
	logger.L.Info("cors_configured",
		slog.String("app_env", cfg.AppEnv),
		slog.String("app_domain", cfg.AppDomain),
		slog.String("frontend_url", cfg.FrontendURL),
		slog.String("central_frontend_url", cfg.CentralFrontendURL),
		slog.Any("base_hosts", corsMatcher.BaseHosts()),
		slog.Int("exact_origins_count", corsMatcher.ExactCount()),
	)
	app.Use(middleware.SecurityHeaders())
	app.Use(middleware.RequestID())
	app.Use(middleware.RequestLogger())

	// Health y métricas (sin TenantResolver, rate limit ni auth)
	app.Get("/", health.Liveness)
	app.Get("/health", health.Readiness)
	app.Get("/metrics", health.Metrics)

	// CORS antes de rate limits: preflight OPTIONS debe recibir Allow-Origin si el origen es válido.
	app.Use(cors.New(cors.Config{
		AllowOriginsFunc: corsMatcher.Allow,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Tenant-Slug"},
		AllowCredentials: true,
		MaxAge:           3600,
	}))

	middleware.ApplyRateLimits(app)

	// Consulta DNI/RUC (público): valida tenant_ruc en central antes de llamar a apiperu
	consultaH := consultaHandler.NewConsultaHandler()
	app.Post("/api/consulta/dni", consultaH.PublicConsultaDNIAPI)
	app.Post("/api/consulta/ruc", consultaH.PublicConsultaRUCAPI)

	// Archivos subidos por tenant (uploads/tenants/{RUC}/...)
	app.All("/uploads/*", tenantstorage.UploadsHandler)

	// Middleware global de resolución de tenant por subdominio / header
	app.Use(middleware.TenantResolver())

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
		return c.JSON(fiber.Map{
			"slug":                 tenant.Slug,
			"name":                 tenant.Name,
			"token_consulta_datos": tenant.TokenConsultaDatos,
		})
	})

	// Super Admin login
	superadmin.RegisterRoutes(app)

	// Tenant login
	auth.RegisterRoutes(app)

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
	tenantAPI := app.Group("/api", middleware.TenantAuthAPI(), middleware.ValidateTenantBinding())

	ubigeoTenant := ubigeo.NewTenantHandler()
	tenantAPI.Get("/ubigeo/regiones", ubigeoTenant.RegionesAPI)
	tenantAPI.Get("/ubigeo/provincias", ubigeoTenant.ProvinciasAPI)
	tenantAPI.Get("/ubigeo/distritos", ubigeoTenant.DistritosAPI)

	dashboard.RegisterRoutes(tenantAPI)
	company.RegisterRoutes(tenantAPI)
	users.RegisterRoutes(tenantAPI)
	contacts.RegisterRoutes(tenantAPI)
	products.RegisterRoutes(tenantAPI)
	inventory.RegisterRoutes(tenantAPI)
	sales.RegisterRoutes(tenantAPI)
	memberships.RegisterRoutes(tenantAPI)
	billing.RegisterRoutes(tenantAPI)
	purchases.RegisterRoutes(tenantAPI)
	cashbank.RegisterRoutes(tenantAPI)
	restaurant.RegisterRoutes(tenantAPI)
	restaurant.RegisterSalePaymentRoutes(tenantAPI)
	modules.RegisterRoutes(tenantAPI)

	// Catch-all
	app.Use(func(c fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Ruta no encontrada"})
	})
}
