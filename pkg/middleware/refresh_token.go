package middleware

import (
	"errors"
	"strings"
	"time"

	"tukifac/config"

	"github.com/golang-jwt/jwt/v5"
)

// RefreshSessionTTL vida del refresh token tenant. Es largo (30 días) a propósito: mientras el
// usuario siga activo, su sesión se renueva sin volver a pedir contraseña. La revocación efectiva
// se logra al refrescar (se reconstruyen los claims desde la BD: si el usuario está desactivado o
// la empresa ya no existe, el refresh falla y el cliente cae al login).
const RefreshSessionTTL = 30 * 24 * time.Hour

const tenantRefreshType = "tenant_refresh"

// ErrInvalidRefreshToken refresh token ausente, mal firmado, de otro tipo o expirado.
var ErrInvalidRefreshToken = errors.New("refresh token inválido o expirado")

// TenantRefreshClaims payload mínimo del refresh token. NO lleva permisos ni módulos: esos se
// reconstruyen desde la BD al refrescar, para reflejar cambios de rol/plan/sucursal.
type TenantRefreshClaims struct {
	UserID        uint   `json:"user_id"`
	TenantSlug    string `json:"tenant_slug"`
	TenantDB      string `json:"tenant_db"`
	TenantID      uint   `json:"tenant_id"`
	TenantVersion uint   `json:"tenant_version"`
	AuthMethod    string `json:"auth_method,omitempty"`
	Type          string `json:"type"` // "tenant_refresh"
	jwt.RegisteredClaims
}

// BuildTenantRefreshToken firma un refresh token para el usuario/empresa dados.
func BuildTenantRefreshToken(userID uint, tenantSlug, tenantDB string, tenantID uint, authMethod string) (string, error) {
	now := time.Now()
	if strings.TrimSpace(authMethod) == "" {
		authMethod = "pwd"
	}
	claims := &TenantRefreshClaims{
		UserID:        userID,
		TenantSlug:    tenantSlug,
		TenantDB:      tenantDB,
		TenantID:      tenantID,
		TenantVersion: CurrentTenantJWTVersion(),
		AuthMethod:    authMethod,
		Type:          tenantRefreshType,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(RefreshSessionTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.AppConfig.JWTSecret))
}

// ParseTenantRefreshToken valida firma, tipo, expiración y aislamiento mínimo del refresh token.
func ParseTenantRefreshToken(tokenStr string) (*TenantRefreshClaims, error) {
	tokenStr = strings.TrimSpace(tokenStr)
	if tokenStr == "" {
		return nil, ErrInvalidRefreshToken
	}
	claims := &TenantRefreshClaims{}
	t, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(config.AppConfig.JWTSecret), nil
	})
	if err != nil || !t.Valid || claims.Type != tenantRefreshType {
		return nil, ErrInvalidRefreshToken
	}
	if claims.TenantID == 0 || strings.TrimSpace(claims.TenantSlug) == "" || strings.TrimSpace(claims.TenantDB) == "" {
		return nil, ErrInvalidRefreshToken
	}
	return claims, nil
}
