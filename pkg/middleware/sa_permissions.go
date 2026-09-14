package middleware

// RequireSAPermission — autorización granular del RBAC central (panel SuperAdmin).
//
// Debe usarse SIEMPRE encadenado DESPUÉS de SuperAdminAuthAPI() (nunca solo), porque no vuelve a
// tocar la BD ni revalida sesión — confía en que c.Locals("sa_claims") ya pasó por
// verifySuperAdminSession (firma, expiración, usuario existente, Active, TokenVersion) al momento
// de setearse. Este archivo NO implementa autenticación, solo autorización.
//
// Orden de la cadena en las rutas: SuperAdminAuthAPI() → RequireSAPermission("modulo.accion").
//
// El bypass de Role=="superadmin" es EXCLUSIVAMENTE de autorización — el superadmin sigue
// pasando por verifySuperAdminSession igual que cualquier usuario (Active/DeletedAt/TokenVersion/
// firma/expiración) en SuperAdminAuthAPI antes de llegar aquí; si esa validación ya rechazó la
// request, este middleware nunca se ejecuta.

import (
	"strings"

	"github.com/gofiber/fiber/v3"
)

// RequireSAPermission verifica que el usuario del panel central tenga el permiso indicado
// (formato "modulo.accion"). superadmin real bypassea esta comprobación (no la de sesión, que ya
// pasó en SuperAdminAuthAPI); el resto de usuarios depende exclusivamente de
// claims.Permissions — nunca de claims.Role == "admin" ni de ningún otro atajo.
func RequireSAPermission(permission string) fiber.Handler {
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("sa_claims").(*SuperAdminClaims)
		if !ok || claims == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Sin contexto de autenticación"})
		}
		if HasSAPermission(claims, permission) {
			return c.Next()
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":      "No tienes permiso para esta acción",
			"permission": permission,
		})
	}
}

// HasSAPermission es la misma comprobación que usa RequireSAPermission (bypass exacto de
// superadmin + saHasPermission), exportada para los pocos casos donde el permiso NO se puede
// fijar a nivel de ruta y debe resolverse dentro del handler — hoy, únicamente las acciones
// fiscales con :action dinámico (ver internal/superadmin/handler/fiscal_handler.go,
// requiredPermissionForFiscalAction). Debe llamarse DESPUÉS de leer claims desde
// c.Locals("sa_claims") (ya seteado por SuperAdminAuthAPI) y SIEMPRE antes de ejecutar el efecto
// de la acción — nunca después.
func HasSAPermission(claims *SuperAdminClaims, permission string) bool {
	if claims == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(claims.Role), "superadmin") {
		return true
	}
	return saHasPermission(claims.Permissions, permission)
}

// CanDelegateAll es el techo de delegación del RBAC central: "un usuario puede otorgar/asignar/
// conservar en otro únicamente los permisos que él mismo puede ejercer". NO es lo mismo que
// HasSAPermission — HasSAPermission responde "¿puedo EJERCER X?"; CanDelegateAll responde
// "¿puedo DELEGAR X (agregarlo a un rol, asignarle a alguien un rol que lo incluya)?". Hoy ambas
// preguntas se resuelven igual (un permiso solo se delega si el propio actor lo tiene, sin
// excepción salvo el bypass de superadmin), así que CanDelegateAll se implementa en términos de
// HasSAPermission — pero deben seguir siendo dos funciones separadas: si el día de mañana se
// necesitara una regla de delegación distinta de la de ejercicio (p. ej. "puedo ejercer X pero no
// delegarlo"), el cambio va aquí, sin tocar ninguna ruta de autorización existente.
//
// ÚNICA implementación de esta regla en todo el RBAC central — Grupo 7 (Fase 5, etapa 3) la usa
// desde tres puntos (SetRolePermissionsAPI, DeleteAPI de roles, asignación de RoleID a un
// usuario); ningún handler debe reimplementar esta lógica a mano.
//
// keys vacío → true (delegar "nada" siempre es válido, p. ej. un diff sin permisos agregados).
// claims nil → false (sin contexto de autenticación, no se puede asumir ninguna capacidad).
func CanDelegateAll(claims *SuperAdminClaims, keys []string) bool {
	if claims == nil {
		return len(keys) == 0
	}
	for _, k := range keys {
		if !HasSAPermission(claims, k) {
			return false
		}
	}
	return true
}

// RequireSuperAdminOnly exige el bypass exacto de superadmin (Role=="superadmin", comparación
// exacta) — SIN excepción, SIN permiso otorgable. Reservado para las dos operaciones que quedaron
// deliberadamente FUERA del sistema de permisos granular (Fase 0/1, ratificado en Fase 5 etapa 3):
//   - POST /tenants/:id/destroy-complete (empresas.destroy no existe como permiso del catálogo)
//   - PUT  /saas-settings/operations-key (rotar la clave que protege destroy-complete)
//
// A diferencia de RequireSAPermission, esto NO admite que un rol granular lo conceda — ni
// siquiera con Permissions=["*"] de un usuario no-superadmin (si alguna vez existiera un bug que
// permitiera eso, este middleware seguiría exigiendo el Role exacto). Debe usarse encadenado
// después de SuperAdminAuthAPI(), igual que RequireSAPermission.
func RequireSuperAdminOnly() fiber.Handler {
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("sa_claims").(*SuperAdminClaims)
		if !ok || claims == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Sin contexto de autenticación"})
		}
		if strings.EqualFold(strings.TrimSpace(claims.Role), "superadmin") {
			return c.Next()
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Esta operación está reservada al superadmin"})
	}
}

// saManageImpliedActions — regla ".manage" del RBAC central, EXPLÍCITA por diseño (allowlist,
// no "implica todo salvo excepciones"): un módulo nuevo, o una acción nueva agregada a un módulo
// existente, nunca queda implícitamente concedida por ".manage" a menos que alguien la agregue
// aquí a mano. Solo se listan los 4 módulos del catálogo (Fase 1) que realmente tienen un
// permiso ".manage" — no se inventa uno para módulos que no lo necesitan (migraciones, pagos,
// fiscal, empresas, planes, suscripciones, usuarios_central NO tienen ".manage" en el catálogo,
// así que sus acciones — incluidas las críticas: destroy, master_access, repair, backfill,
// refund, cancel — son siempre explícitas, no hay nada de qué excluirlas).
//
//   - facturador.manage → view, sync (ninguna acción crítica en este módulo)
//   - documentos.manage → view — NUNCA approve_purchase (crítico, permiso independiente)
//   - ajustes.manage    → view (la rotación de operations-key ni siquiera es un permiso: ver
//     sa_rbac_seed.go, queda reservada al bypass de superadmin fuera de este sistema)
//   - roles.manage      → view, create, update, delete
var saManageImpliedActions = map[string]map[string]bool{
	"facturador": {"view": true, "sync": true},
	"documentos": {"view": true},
	"ajustes":    {"view": true},
	"roles":      {"view": true, "create": true, "update": true, "delete": true},
}

// saHasPermission indica si el conjunto de permisos del JWT concede el permiso requerido,
// incluyendo la expansión de ".manage" definida en saManageImpliedActions.
func saHasPermission(permissions []string, required string) bool {
	if required == "" {
		return false
	}
	set := make(map[string]struct{}, len(permissions))
	for _, p := range permissions {
		set[p] = struct{}{}
	}
	if _, ok := set["*"]; ok {
		return true
	}
	if _, ok := set[required]; ok {
		return true
	}
	module, action, ok := splitModuleAction(required) // reutiliza el helper de tenant_permissions.go
	if !ok {
		return false
	}
	if _, hasManage := set[module+".manage"]; hasManage {
		return saManageImpliedActions[module][action]
	}
	return false
}
