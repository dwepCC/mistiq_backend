package middleware

import (
	"strings"
	"time"

	"tukifac/config"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"
)

const rateLimitWindow = time.Minute

// RateLimitKey usa IP real (TrustProxy + X-Forwarded-For) y tenant cuando existe.
func RateLimitKey(c fiber.Ctx) string {
	ip := c.IP()
	if slug, ok := c.Locals("tenant_slug").(string); ok && slug != "" {
		return ip + "|" + slug
	}
	return ip
}

func rateLimitResponse(c fiber.Ctx) error {
	return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
		"error": "demasiadas solicitudes, intente de nuevo en un momento",
	})
}

func newLimiter(max int, keyFn func(fiber.Ctx) string) fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        max,
		Expiration: rateLimitWindow,
		KeyGenerator: func(c fiber.Ctx) string {
			if keyFn != nil {
				return keyFn(c)
			}
			return RateLimitKey(c)
		},
		LimitReached: rateLimitResponse,
		// Cuenta también intentos fallidos (brute force en login)
		SkipSuccessfulRequests: false,
		SkipFailedRequests:     false,
	})
}

func conditionalLimiter(match func(fiber.Ctx) bool, max int, keyFn func(fiber.Ctx) string) fiber.Handler {
	inner := newLimiter(max, keyFn)
	return func(c fiber.Ctx) error {
		if !config.AppConfig.RateLimitEnabled || !match(c) {
			return c.Next()
		}
		return inner(c)
	}
}

func skipRateLimitPaths(c fiber.Ctx) bool {
	path := c.Path()
	switch path {
	case "/", "/health", "/health/live", "/metrics":
		return true
	}
	if strings.HasPrefix(path, "/uploads/") {
		return true
	}
	if strings.HasPrefix(path, "/api/products/bulk-import/") {
		return true
	}
	if c.Method() == fiber.MethodOptions {
		return true
	}
	return false
}

// RateLimitGlobal protección general API (300 req/min por IP o IP|tenant).
func RateLimitGlobal() fiber.Handler {
	cfg := config.AppConfig
	return conditionalLimiter(func(c fiber.Ctx) bool {
		return !skipRateLimitPaths(c)
	}, cfg.RateLimitGlobal, RateLimitKey)
}

// isAuthSensitivePath rutas reales de autenticación y contraseña (no hay refresh/forgot en el código).
func isAuthSensitivePath(path string) bool {
	switch path {
	case "/api/login", "/api/superadmin/login":
		return true
	}
	return strings.HasSuffix(path, "/password")
}

func isPublicConsultPath(path string) bool {
	return path == "/api/consulta/dni" || path == "/api/consulta/ruc"
}

func isBillingPath(path string) bool {
	return strings.HasPrefix(path, "/api/billing/")
}

func isUploadPath(path, method string) bool {
	if method != fiber.MethodPost {
		return false
	}
	if path == "/api/superadmin/payments" {
		return true
	}
	return strings.HasSuffix(path, "/image") || strings.HasSuffix(path, "/photo")
}

// RateLimitAuth login y endpoints sensibles (10 req/min por IP).
func RateLimitAuth() fiber.Handler {
	cfg := config.AppConfig
	return conditionalLimiter(func(c fiber.Ctx) bool {
		return isAuthSensitivePath(c.Path())
	}, cfg.RateLimitAuth, func(c fiber.Ctx) string { return c.IP() })
}

// RateLimitPublicConsult consulta DNI/RUC pública (20 req/min por IP).
func RateLimitPublicConsult() fiber.Handler {
	cfg := config.AppConfig
	return conditionalLimiter(func(c fiber.Ctx) bool {
		return isPublicConsultPath(c.Path())
	}, cfg.RateLimitPublicConsult, func(c fiber.Ctx) string { return c.IP() })
}

// RateLimitBilling emisión SUNAT y documentos (60 req/min por IP|tenant).
func RateLimitBilling() fiber.Handler {
	cfg := config.AppConfig
	return conditionalLimiter(func(c fiber.Ctx) bool {
		return isBillingPath(c.Path())
	}, cfg.RateLimitBilling, RateLimitKey)
}

// RateLimitUpload multipart imágenes y comprobantes (30 req/min por IP|tenant).
func RateLimitUpload() fiber.Handler {
	cfg := config.AppConfig
	return conditionalLimiter(func(c fiber.Ctx) bool {
		return isUploadPath(c.Path(), c.Method())
	}, cfg.RateLimitUpload, RateLimitKey)
}

// ApplyRateLimits registra la cadena de limiters (orden: específicos antes del global).
func ApplyRateLimits(app *fiber.App) {
	if !config.AppConfig.RateLimitEnabled {
		return
	}
	app.Use(RateLimitAuth())
	app.Use(RateLimitPublicConsult())
	app.Use(RateLimitBilling())
	app.Use(RateLimitUpload())
	app.Use(RateLimitGlobal())
}
