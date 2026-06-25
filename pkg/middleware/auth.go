package middleware

import (
	"fmt"
	"strings"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/saas"
	"tukifac/pkg/tenantctx"
	"tukifac/pkg/tenantstorage"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

// TenantClaims es el payload del JWT del tenant.
// Contiene todo lo necesario para autorizar el request sin consultar la BD central.
type TenantClaims struct {
	UserID      uint     `json:"user_id"`
	Email       string   `json:"email"`
	RoleID      uint     `json:"role_id"`
	RoleName    string   `json:"role_name"`
	TenantSlug  string   `json:"tenant_slug"`
	TenantDB    string   `json:"tenant_db"`
	TenantID    uint     `json:"tenant_id"`    // ID del tenant en BD central
	TenantVersion uint   `json:"tenant_version"` // >= MinTenantJWTVersion; invalida tokens legacy
	PlanID      uint     `json:"plan_id"`      // Plan activo al momento del login
	Modules     []string `json:"modules"`      // Módulos habilitados (fuente: BD central al login)
	Permissions []string `json:"permissions"`  // Permisos del rol en formato "module.action"
	EmployeeType   string `json:"employee_type"` // admin, cashier, waiter, cook, driver, supervisor
	AuthMethod     string `json:"am,omitempty"`    // pwd | pin (vacío = pwd)
	PermVer        uint   `json:"pv,omitempty"`    // versión cache permisos restaurante
	StaffID        uint   `json:"sid,omitempty"`   // tenant_restaurant_staff.id
	Status         string `json:"status"`          // Estado del tenant al momento del login
	Type        string   `json:"type"`         // "tenant"
	ActiveBranchID       uint `json:"active_branch_id"`
	BranchSessionVersion uint `json:"branch_session_version"`
	jwt.RegisteredClaims
}

// Claims para super admin
type SuperAdminClaims struct {
	UserID uint   `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Type   string `json:"type"` // "superadmin"
	jwt.RegisteredClaims
}

// TenantAuthWeb protege rutas web del tenant (cookie token)
func TenantAuthWeb() fiber.Handler {
	return func(c fiber.Ctx) error {
		token := c.Cookies("token")
		if token == "" {
			return c.Redirect().To("/login")
		}

		claims := &TenantClaims{}
		t, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(config.AppConfig.JWTSecret), nil
		})
		if err != nil || !t.Valid || claims.Type != "tenant" {
			c.ClearCookie("token")
			return c.Redirect().To("/login")
		}

		c.Locals("user_id", claims.UserID)
		c.Locals("user_email", claims.Email)
		c.Locals("user_role_id", claims.RoleID)
		c.Locals("user_role", claims.RoleName)
		c.Locals("user_name", claims.Email)
		c.Locals("permissions", claims.Permissions)
		c.Locals("is_dev", config.AppConfig.IsDev())
		return c.Next()
	}
}

// TenantAuthAPI protege rutas API del tenant.
// Acepta Bearer token en Authorization header O cookie "token".
// Valida: token válido, tipo "tenant", y status == "active" extraído del JWT.
// NO consulta la BD central en cada request — el JWT es la fuente de verdad durante la sesión.
func TenantAuthAPI() fiber.Handler {
	publicPaths := map[string]bool{
		"/api/login":            true,
		"/api/superadmin/login": true,
	}

	return func(c fiber.Ctx) error {
		if publicPaths[c.Path()] {
			return c.Next()
		}

		// 1. Bearer token del header Authorization
		tokenStr := ""
		authHeader := c.Get("Authorization")
		if authHeader != "" {
			parts := strings.Split(authHeader, " ")
			if len(parts) == 2 && parts[0] == "Bearer" && parts[1] != "null" && parts[1] != "" {
				tokenStr = parts[1]
			}
		}

		// 2. Fallback: cookie "token"
		if tokenStr == "" {
			tokenStr = c.Cookies("token")
		}
		if tokenStr == "" {
			tokenStr = strings.TrimSpace(c.Query("access_token")) // SSE EventSource (?access_token=)
		}

		if tokenStr == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Token no proporcionado"})
		}

		claims := &TenantClaims{}
		t, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(config.AppConfig.JWTSecret), nil
		})
		if err != nil || !t.Valid || claims.Type != "tenant" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Token inválido o expirado"})
		}

		if err := validateTenantJWTClaims(claims); err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": err.Error(),
				"code":  "TOKEN_TENANT_INVALID",
			})
		}

		if err := bindTenantFromJWTClaimsIfMissing(c, claims); err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Empresa del token no encontrada",
				"code":  "TOKEN_TENANT_INVALID",
			})
		}

		tenant, _ := c.Locals("tenant").(*database.Tenant)
		path := c.Path()
		method := c.Method()

		// Billing Hub: suspendido/bloqueado pueden GET; solo suspendido/active pueden POST pago.
		if IsSubscriptionHubPath(path) {
			if tenant == nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Contexto de empresa no encontrado"})
			}
			if !TenantAllowsBillingHubRead(tenant) {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error":  "Acceso al portal de pagos no permitido",
					"status": tenant.Status,
				})
			}
			if IsSubscriptionPaymentSubmit(path, method) {
				if err := saas.CanTenantSubmitPayment(tenant); err != nil {
					return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
						"error": err.Error(),
						"code":  "PAYMENT_BLOCKED",
					})
				}
			}
		} else if tenant != nil {
			// ERP: estado en tiempo real (provisional, grace, día de vencimiento).
			view, err := saas.GetTenantView(tenant.ID)
			if err != nil || !view.CanOperate {
				return c.Status(fiber.StatusPaymentRequired).JSON(fiber.Map{
					"error": "Acceso operativo restringido por suscripción",
					"code":  "SUBSCRIPTION_REQUIRED",
				})
			}
		}

		// Inyectar datos en Locals para handlers y middleware de módulos
		c.Locals("user_id", claims.UserID)
		c.Locals("user_email", claims.Email)
		c.Locals("user_role_id", claims.RoleID)
		c.Locals("user_role", claims.RoleName)
		c.Locals("employee_type", claims.EmployeeType)
		c.Locals("tenant_claims", claims) // acceso completo a claims para RequireModule
		c.Locals("permissions", claims.Permissions)
		return c.Next()
	}
}

// RequireModule verifica que el módulo requerido esté habilitado en el JWT del tenant.
// Usar después de TenantAuthAPI en rutas específicas.
//
// Ejemplo de uso:
//
//	api.Get("/billing/invoices", middleware.RequireModule("billing"), handler.ListInvoices)
func RequireModule(moduleKey string) fiber.Handler {
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("tenant_claims").(*TenantClaims)
		if !ok || claims == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Sin contexto de autenticación",
			})
		}
		for _, m := range claims.Modules {
			if m == moduleKey {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  fmt.Sprintf("El módulo '%s' no está habilitado en tu plan", moduleKey),
			"module": moduleKey,
		})
	}
}

// SuperAdminAuthWeb protege rutas web del panel super admin (cookie sa_token)
func SuperAdminAuthWeb() fiber.Handler {
	return func(c fiber.Ctx) error {
		token := c.Cookies("sa_token")
		if token == "" {
			return c.Redirect().To("/superadmin/login")
		}

		claims := &SuperAdminClaims{}
		t, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(config.AppConfig.SAJWTSecret), nil
		})
		if err != nil || !t.Valid || claims.Type != "superadmin" {
			c.ClearCookie("sa_token")
			return c.Redirect().To("/superadmin/login")
		}

		c.Locals("sa_user_id", claims.UserID)
		c.Locals("sa_user_email", claims.Email)
		c.Locals("sa_user_role", claims.Role)
		return c.Next()
	}
}

// SuperAdminAuthAPI protege rutas API del super admin (Bearer token)
func SuperAdminAuthAPI() fiber.Handler {
	return func(c fiber.Ctx) error {
		// El login del superadmin es público — no requiere token
		if c.Path() == "/api/superadmin/login" {
			return c.Next()
		}

		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Token no proporcionado"})
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Formato de token inválido"})
		}

		claims := &SuperAdminClaims{}
		t, err := jwt.ParseWithClaims(parts[1], claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(config.AppConfig.SAJWTSecret), nil
		})
		if err != nil || !t.Valid || claims.Type != "superadmin" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Token inválido o expirado"})
		}

		c.Locals("sa_user_id", claims.UserID)
		c.Locals("sa_user_email", claims.Email)
		c.Locals("sa_user_role", claims.Role)
		return c.Next()
	}
}

// RequireRestaurantAdminOrTenantAdmin gestión staff/config (permiso s.m o admin tenant).
func RequireRestaurantAdminOrTenantAdmin() fiber.Handler {
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("tenant_claims").(*TenantClaims)
		if !ok || claims == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Sin contexto de autenticación"})
		}
		if claims.RoleName == "Administrador" {
			return c.Next()
		}
		if HasRestaurantPerm(c, "s.m") {
			return c.Next()
		}
		et := claims.EmployeeType
		if et == "admin" || et == "supervisor" {
			return c.Next()
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "Se requiere permiso de administración del restaurante",
		})
	}
}

// bindTenantFromJWTClaimsIfMissing resuelve tenant cuando EventSource/SSE u otros clientes
// no envían subdominio ni X-Tenant-Slug (p. ej. localhost en dev). El JWT ya fue validado;
// ValidateTenantBinding verifica coherencia slug/db/id.
func bindTenantFromJWTClaimsIfMissing(c fiber.Ctx, claims *TenantClaims) error {
	if _, ok := tenantctx.Tenant(c); ok {
		return nil
	}
	if claims == nil {
		return fmt.Errorf("claims requeridos")
	}
	slug := strings.TrimSpace(claims.TenantSlug)
	if slug == "" {
		return nil
	}
	tenant, err := LookupTenantBySlug(slug)
	if err != nil {
		return err
	}
	tenantDB, err := database.GetTenantDB(tenant.DBName)
	if err != nil {
		return err
	}
	tenantctx.Bind(c, tenant, tenantDB)
	if ruc := tenantstorage.SanitizeRUC(tenant.RUC); ruc != "" {
		c.Locals("tenant_ruc", ruc)
	}
	return nil
}

// RequireRole verifica que el usuario tenga uno de los roles especificados
func RequireRole(roles ...string) fiber.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(c fiber.Ctx) error {
		role, _ := c.Locals("user_role").(string)
		if len(allowed) == 0 {
			return c.Next()
		}
		if _, ok := allowed[role]; !ok {
			return c.Status(fiber.StatusForbidden).SendString("No tienes permisos para esta acción")
		}
		return c.Next()
	}
}
