package middleware

import (
	"strconv"
	"strings"
	"time"

	"tukifac/config"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"github.com/golang-jwt/jwt/v5"
)

const rateLimitWindow = time.Minute

// RateLimitKey = IP REAL del cliente (ClientIP: CF-Connecting-IP solo si la conexión viene de Cloudflare) y,
// cuando la petición trae un JWT de tenant VÁLIDO, el id de ese tenant.
//
// Antes la clave era c.IP() = la IP del edge de Cloudflare: todos los usuarios de todos los tenants que
// entraban por el mismo edge compartían el mismo cupo (300/min global, 60/min facturación). Ahora cada
// cliente tiene su cupo, y dos tenants detrás de la misma IP (oficina compartida, CGNAT móvil) no se pisan.
//
// El tenant sale SOLO de un JWT con firma verificada, nunca de X-Tenant-Slug/Host: son datos que el cliente
// controla y rotarlos daría cupos infinitos. Este limitador corre antes de resolver el tenant a propósito
// (frena el abuso antes de gastar consultas), por eso no se usa tenant_slug de Locals.
func RateLimitKey(c fiber.Ctx) string {
	ip := ClientIP(c)
	if id := verifiedTenantID(c); id > 0 {
		return ip + "|t" + strconv.FormatUint(uint64(id), 10)
	}
	return ip
}

// verifiedTenantID devuelve el tenant_id del JWT de la petición solo si la firma y el tipo son válidos; 0 si no
// hay token o no es válido (ese caso se limita solo por IP).
func verifiedTenantID(c fiber.Ctx) uint {
	tokenStr := ""
	if h := c.Get("Authorization"); h != "" {
		if parts := strings.Split(h, " "); len(parts) == 2 && parts[0] == "Bearer" && parts[1] != "null" {
			tokenStr = parts[1]
		}
	}
	if tokenStr == "" {
		tokenStr = c.Cookies("token")
	}
	if tokenStr == "" {
		tokenStr = queryAccessToken(c, false) // EventSource (SSE): solo /api/billing/events
	}
	if tokenStr == "" {
		return 0
	}
	claims := &TenantClaims{}
	t, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(config.AppConfig.JWTSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !t.Valid || claims.Type != "tenant" {
		return 0
	}
	return claims.TenantID
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
	case "/", "/health", "/health/live", "/api/health/live", "/metrics":
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
	case "/api/login", "/api/superadmin/login", "/api/auth/master-exchange":
		return true
	}
	return strings.HasSuffix(path, "/password")
}

func isPublicConsultPath(path string) bool {
	return path == "/api/consulta/dni" || path == "/api/consulta/ruc" || path == "/api/public/ecommerce/track" || path == "/api/public/ecommerce/track/voucher"
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
	}, cfg.RateLimitAuth, ClientIP)
}

// RateLimitPublicConsult consulta DNI/RUC pública (20 req/min por IP).
func RateLimitPublicConsult() fiber.Handler {
	cfg := config.AppConfig
	return conditionalLimiter(func(c fiber.Ctx) bool {
		return isPublicConsultPath(c.Path())
	}, cfg.RateLimitPublicConsult, ClientIP)
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
