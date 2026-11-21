package middleware

import (
	"errors"
	"strings"

	"tukifac/config"

	"github.com/golang-jwt/jwt/v5"
)

// TenantRefreshType valor de claims.Type de un refresh token tenant.
//
// La emisión (firma HS256) y la vigencia (30 días) viven en internal/auth/service/issuer.go, único
// emisor de tokens tenant. La revocación efectiva se logra al refrescar: se reconstruyen los claims
// desde la BD y, si el usuario está desactivado o la empresa ya no existe, el refresh falla y el
// cliente cae al login.
const TenantRefreshType = "tenant_refresh"

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

// ParseTenantRefreshToken valida firma (solo HS256), tipo, expiración, versión y aislamiento mínimo del refresh token.
func ParseTenantRefreshToken(tokenStr string) (*TenantRefreshClaims, error) {
	tokenStr = strings.TrimSpace(tokenStr)
	if tokenStr == "" {
		return nil, ErrInvalidRefreshToken
	}
	claims := &TenantRefreshClaims{}
	t, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(config.AppConfig.JWTSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !t.Valid || claims.Type != TenantRefreshType {
		return nil, ErrInvalidRefreshToken
	}
	// Mismo criterio que los access tokens: una versión de emisión anterior a la mínima se descarta.
	if claims.TenantVersion < MinTenantJWTVersion {
		return nil, ErrInvalidRefreshToken
	}
	if claims.TenantID == 0 || strings.TrimSpace(claims.TenantSlug) == "" || strings.TrimSpace(claims.TenantDB) == "" {
		return nil, ErrInvalidRefreshToken
	}
	return claims, nil
}
