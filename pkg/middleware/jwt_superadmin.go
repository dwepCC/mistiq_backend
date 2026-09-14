package middleware

// Fase 4 del RBAC central: el JWT de SuperAdmin deja de ser la única fuente de verdad de la
// sesión. En cada request autenticado se vuelve a consultar SuperAdminUser en BD para confirmar
// que el usuario sigue existiendo, sigue activo, y que el token no fue revocado (TokenVersion).
//
// Role == "superadmin" SIGUE siendo el bypass total del sistema de PERMISOS (RBAC granular), pero
// NUNCA es un bypass de AUTENTICACIÓN: un superadmin desactivado, eliminado o con una sesión
// revocada queda tan rechazado como cualquier otro usuario. Ningún camino de este archivo lee
// claims.Role para saltarse Active/DeletedAt/TokenVersion — ver verifySuperAdminSession.

import (
	"errors"

	"tukifac/config"
	"tukifac/pkg/database"
)

// MinSuperAdminJWTVersion — versión mínima del claim sa_jwt_version. Los tokens de SuperAdmin
// emitidos ANTES de esta fase no traen ese claim (se deserializan como 0), así que quedan por
// debajo del mínimo y se rechazan en producción, forzando un nuevo login sin necesitar ninguna
// migración de datos. Mismo patrón que MinTenantJWTVersion/CurrentTenantJWTVersion (ver
// jwt_tenant.go) — igual que allí, el piso solo se exige en producción para no forzar relogin
// constante en desarrollo cada vez que se toca este archivo.
const MinSuperAdminJWTVersion uint = 1

// CurrentSuperAdminJWTVersion es la versión embebida en los tokens nuevos.
func CurrentSuperAdminJWTVersion() uint {
	return MinSuperAdminJWTVersion
}

var (
	// Token estructuralmente válido (firma y expiración OK) pero emitido antes de esta fase.
	ErrLegacySuperAdminToken = errors.New("token obsoleto: inicie sesión nuevamente")
	// El usuario del claim ya no existe, o fue eliminado (soft-delete) — First() de GORM ya
	// excluye filas con deleted_at, así que ambos casos llegan aquí sin distinción.
	ErrSuperAdminUserNotFound = errors.New("usuario no encontrado")
	ErrSuperAdminUserInactive = errors.New("usuario desactivado")
	// TokenVersion del JWT ya no coincide con la de BD: la sesión fue revocada explícitamente
	// (cambio de rol, reset/cambio de contraseña, desactivación, etc. — ver database.SuperAdminUser.IncrementTokenVersion).
	ErrSuperAdminTokenRevoked = errors.New("sesión inválida, inicie sesión nuevamente")
)

// validateSuperAdminJWTVersion rechaza tokens legacy sin sa_jwt_version en producción (ver
// comentario de MinSuperAdminJWTVersion).
func validateSuperAdminJWTVersion(claims *SuperAdminClaims) error {
	if config.AppConfig != nil && config.AppConfig.IsProd() {
		if claims.SAJWTVersion < MinSuperAdminJWTVersion {
			return ErrLegacySuperAdminToken
		}
	}
	return nil
}

// verifySuperAdminSession confirma contra BD que el usuario del JWT sigue siendo una sesión
// válida. Se consulta en cada request (ver nota de rendimiento en auth.go) — deliberado: la
// seguridad de Active/TokenVersion no puede depender solo de lo que dice el JWT.
func verifySuperAdminSession(claims *SuperAdminClaims) (*database.SuperAdminUser, error) {
	if database.CentralDB == nil {
		return nil, ErrSuperAdminUserNotFound
	}
	var user database.SuperAdminUser
	if err := database.CentralDB.First(&user, claims.UserID).Error; err != nil {
		return nil, ErrSuperAdminUserNotFound
	}
	if !user.Active {
		return nil, ErrSuperAdminUserInactive
	}
	if user.TokenVersion != claims.TokenVersion {
		return nil, ErrSuperAdminTokenRevoked
	}
	return &user, nil
}
