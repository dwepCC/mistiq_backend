package middleware

import (
	"errors"
	"strings"
	"time"

	"tukifac/config"
	"tukifac/pkg/tenantctx"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

// EcommerceCustomerJWTType valor de Type en el JWT del cliente final — nunca "tenant" ni
// "superadmin", así que aunque alguien intentara reusar este token contra TenantAuthAPI/
// SuperAdminAuthAPI, la firma ya falla primero (secreto distinto, EcommerceCustomerJWTSecret vs
// JWTSecret/SAJWTSecret — aislamiento real, no solo un chequeo de campo que se pudiera olvidar).
const EcommerceCustomerJWTType = "ecommerce_customer"

// EcommerceCustomerTokenTTL sesión larga (mismo criterio que RefreshSessionTTL de staff, 30 días):
// es una cuenta ligera de tienda, no tiene sentido pedir login constantemente. Sin mecanismo de
// revocación en esta fase (sin TokenVersion/columna de sesión) — logout es solo client-side
// (borra el token guardado). Documentado como limitación conocida, no como descuido: agregar
// revocación real es una extensión aislada (una columna + un chequeo acá) si se necesita después.
const EcommerceCustomerTokenTTL = 30 * 24 * time.Hour

// EcommerceCustomerClaims payload del JWT de cliente final. Deliberadamente mínimo (cuenta
// "ligera", Contrato ecommerce v2): sin permisos, sin módulos, sin nada que se parezca a
// TenantClaims — un token de cliente JAMÁS debe poder confundirse con uno de staff.
type EcommerceCustomerClaims struct {
	CustomerAccountID uint   `json:"customer_account_id"`
	Phone             string `json:"phone"`
	TenantSlug        string `json:"tenant_slug"`
	Type              string `json:"type"` // "ecommerce_customer"
	jwt.RegisteredClaims
}

// BuildEcommerceCustomerToken firma el JWT de sesión del cliente final tras registro/login.
func BuildEcommerceCustomerToken(customerAccountID uint, phone, tenantSlug string) (string, error) {
	now := time.Now()
	claims := EcommerceCustomerClaims{
		CustomerAccountID: customerAccountID,
		Phone:             phone,
		TenantSlug:        tenantSlug,
		Type:              EcommerceCustomerJWTType,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(EcommerceCustomerTokenTTL)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.AppConfig.EcommerceCustomerJWTSecret))
}

var errInvalidCustomerToken = errors.New("sesión inválida o expirada")

func parseEcommerceCustomerToken(c fiber.Ctx) (*EcommerceCustomerClaims, error) {
	tokenStr := ""
	if auth := c.Get("Authorization"); auth != "" {
		parts := strings.Split(auth, " ")
		if len(parts) == 2 && parts[0] == "Bearer" {
			tokenStr = parts[1]
		}
	}
	if tokenStr == "" {
		return nil, errInvalidCustomerToken
	}
	claims := &EcommerceCustomerClaims{}
	t, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(config.AppConfig.EcommerceCustomerJWTSecret), nil
	})
	if err != nil || !t.Valid || claims.Type != EcommerceCustomerJWTType {
		return nil, errInvalidCustomerToken
	}
	// Coherencia de tenant: el token se firmó para un slug específico — si el request resolvió a
	// OTRO tenant (dominio/subdominio distinto), se rechaza. Sin esto, un token válido robado de un
	// tenant podría reusarse tal cual contra la tienda de otro tenant (misma firma global).
	if slug := tenantctx.Slug(c); slug != "" && !strings.EqualFold(slug, claims.TenantSlug) {
		return nil, errInvalidCustomerToken
	}
	return claims, nil
}

// EcommerceCustomerAuthRequired protege /public/ecommerce/account/* — exige sesión de cliente
// válida. Usar DESPUÉS de RequireTenant()/RequireEcommerceAvailable() en la cadena de rutas.
func EcommerceCustomerAuthRequired() fiber.Handler {
	return func(c fiber.Ctx) error {
		claims, err := parseEcommerceCustomerToken(c)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": err.Error()})
		}
		c.Locals("ecommerce_customer_claims", claims)
		c.Locals("ecommerce_customer_id", claims.CustomerAccountID)
		return c.Next()
	}
}

// EcommerceCustomerAuthOptional NO rechaza si falta el token — el checkout público sirve tanto a
// invitados como a clientes logueados por el mismo endpoint (Contrato v2 Fase 4).
func EcommerceCustomerAuthOptional() fiber.Handler {
	return func(c fiber.Ctx) error {
		if claims, err := parseEcommerceCustomerToken(c); err == nil {
			c.Locals("ecommerce_customer_claims", claims)
			c.Locals("ecommerce_customer_id", claims.CustomerAccountID)
		}
		return c.Next()
	}
}
