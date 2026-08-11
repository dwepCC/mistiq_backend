package middleware

import (
	"errors"
	"strings"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Rutas que no requieren sucursal activa validada (solo auth tenant).
var branchContextSkipPaths = map[string]bool{
	"/api/login":                 true,
	"/api/session/switch-branch": true,
	"/api/session/context":       true,
	"/api/company/branches":      true, // listar sucursales para admin UI
}

// BranchContextMiddleware valida sucursal activa en JWT vs BD y anti-spoofing base.
// Fail-safe: si el tenant aún no tiene columnas multi-sucursal, opera en modo legacy (branch_id).
func BranchContextMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		path := c.Path()
		if branchContextSkipPaths[path] {
			return c.Next()
		}
		if !strings.HasPrefix(path, "/api/") {
			return c.Next()
		}

		claims, ok := c.Locals("tenant_claims").(*TenantClaims)
		if !ok || claims == nil {
			return c.Next()
		}

		tdb, _ := c.Locals("tenantDB").(*gorm.DB)
		if tdb == nil {
			return c.Next()
		}

		user, legacy, err := database.LoadTenantUserForBranch(tdb, claims.UserID)
		if err != nil {
			// Solo el usuario realmente inexistente invalida la sesión (401). Un fallo transitorio
			// de la BD del tenant (pool saturado, conexión caída) NO es sesión inválida: se responde
			// 503 reintentable para que el cliente reintente en vez de forzar el logout.
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Usuario no encontrado"})
			}
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error": "Servicio temporalmente no disponible, reintente",
				"code":  "DB_TRANSIENT",
			})
		}

		if !legacy && claims.BranchSessionVersion > 0 && user.BranchSessionVersion != claims.BranchSessionVersion {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"code":    branch.CodeSessionUpdated,
				"message": "Tu acceso cambió",
			})
		}

		activeID := claims.ActiveBranchID
		if activeID == 0 {
			home, err := branch.ResolveHomeBranchID(tdb, user)
			if err != nil {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"code":    branch.CodeBranchRequired,
					"error":   err.Error(),
					"message": "Configure una sucursal para el usuario",
				})
			}
			activeID = home
		}

		if activeID == 0 {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"code":    branch.CodeBranchRequired,
				"error":   "Sucursal activa requerida",
				"message": "Inicie sesión nuevamente o seleccione sucursal",
			})
		}

		canSwitch := branch.CanSwitchBranch(claims.RoleName, user)
		if !canSwitch {
			home, _ := branch.ResolveHomeBranchID(tdb, user)
			if home > 0 && activeID != home {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"code":    branch.CodeBranchForbidden,
					"error":   "No puede operar en otra sucursal",
					"message": "Su sucursal asignada es la única permitida",
				})
			}
			activeID = home
		}

		c.Locals("active_branch_id", activeID)
		c.Locals("is_branch_admin", canSwitch)
		c.Locals("branch_legacy_mode", legacy)
		return c.Next()
	}
}
