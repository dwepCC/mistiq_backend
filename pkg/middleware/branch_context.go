package middleware

import (
	"errors"
	"strings"

	"tukifac/pkg/branch"
	"tukifac/pkg/branchscope"
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

// RejectLegacyZeroSessionVersion endurece la versión de sesión: con true, un token con claim
// branch_session_version == 0 (emitido antes de V178, cuando la columna se creó sin backfill) se rechaza
// siempre con 409 SESSION_UPDATED (el cliente refresca y recibe un token con versión >= 1).
//
// Con false (valor de despliegue, ventana de compatibilidad de 24 h = vigencia del access token) un claim 0
// se tolera mientras el usuario siga en la versión base (<= 1: 0 sin migrar o 1 tras V178). En cuanto
// ocurre la primera revocación (BumpSessionVersion deja la versión en >= 2) el claim 0 también cae.
// Pasadas 24 h del deploy completo (V178 + backend) se cambia a true y se retira la tolerancia.
const RejectLegacyZeroSessionVersion = false

// sessionVersionStale indica si el claim de versión de sesión quedó desfasado respecto al usuario en BD.
func sessionVersionStale(claimVersion, userVersion uint) bool {
	if claimVersion == 0 {
		if RejectLegacyZeroSessionVersion {
			return true
		}
		return userVersion > 1
	}
	return userVersion != claimVersion
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

		if !legacy && sessionVersionStale(claims.BranchSessionVersion, user.BranchSessionVersion) {
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

		if !user.Active {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Usuario inactivo"})
		}

		// "Ver todas las sucursales" depende del permiso branches.view_all (no del nombre del rol ni de cuántas
		// sucursales tenga asignadas). Tenants aún sin V176 conservan la regla anterior.
		viewAll := branch.RoleViewsAllCached(tdb, claims.RoleID, claims.RoleName, claims.AuthMethod)
		home, _ := branch.ResolveHomeBranchID(tdb, user)
		allowed, _ := branch.GetUserAssignedBranchIDs(tdb, user.ID)
		if len(allowed) == 0 && home > 0 {
			allowed = []uint{home}
		}
		// Un usuario sin ninguna sucursal asignada queda bloqueado hasta corregir su configuración (solo en
		// tenants con V176, que ya asignó la sucursal principal a los existentes).
		if !legacy && !viewAll && branch.PermissionCatalogReady(tdb) && !branch.UserHasAnyBranch(tdb, user) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"code":    branch.CodeBranchRequired,
				"error":   "Usuario sin sucursal asignada",
				"message": "Su usuario no tiene sucursal asignada. Pida al administrador que se la asigne.",
			})
		}
		if !viewAll {
			ok := false
			for _, id := range allowed {
				if id == activeID {
					ok = true
					break
				}
			}
			if !ok {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"code":    branch.CodeBranchForbidden,
					"error":   "No puede operar en otra sucursal",
					"message": "La sucursal activa no está entre las asignadas a su usuario",
				})
			}
		}

		// Modo "Todas las sucursales": solo si se pide explícitamente y el usuario tiene branches.view_all.
		// Es de solo lectura: cualquier escritura exige una sucursal concreta.
		mode := branch.ModeSingleBranch
		if viewAll && strings.EqualFold(c.Get(branch.HeaderBranchMode), "all") {
			mode = branch.ModeAllBranches
			switch c.Method() {
			case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			default:
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"code":    branch.CodeBranchReadOnly,
					"error":   "Todas las sucursales es solo de consulta",
					"message": "Seleccione una sucursal concreta para registrar o modificar datos",
				})
			}
		}
		canSwitch := viewAll || len(allowed) > 1 || branch.RoleHasBranchPermission(tdb, claims.RoleID, "switch")

		c.Locals("active_branch_id", activeID)
		c.Locals("allowed_branch_ids", allowed)
		c.Locals("is_branch_admin", viewAll)
		c.Locals("branch_ctx", &branch.Context{
			UserID: user.ID, ActiveBranchID: activeID, AllowedBranchIDs: allowed,
			CanViewAll: viewAll, CanSwitch: canSwitch, Mode: mode,
		})
		c.Locals("branch_legacy_mode", legacy)

		// Scope global de sucursal: el tenantDB de la solicitud pasa a ser una sesión cuyo contexto lleva el Scope, y los
		// callbacks de pkg/branchscope acotan por sucursal toda consulta/escritura sobre las entidades registradas.
		c.Locals("tenantDB", branchscope.Attach(tdb, &branchscope.Scope{
			UserID: user.ID, ActiveBranchID: activeID, AllowedBranchIDs: allowed,
			ViewAll: viewAll, ReadOnly: mode == branch.ModeAllBranches,
		}))
		return c.Next()
	}
}
