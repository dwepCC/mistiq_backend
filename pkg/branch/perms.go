package branch

import (
	"sync"
	"time"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Permisos RBAC de sucursales (catálogo tenant_permissions, módulo "branches").
const (
	PermViewAll = "branches.view_all" // ver datos consolidados de todas las sucursales (solo lectura)
	PermSwitch  = "branches.switch"   // cambiar de sucursal activa entre las que tiene acceso
	PermManage  = "branches.manage"   // crear/editar/eliminar sucursales y asignar sucursales/roles con acceso total

	// AuthMethodPIN coincide con middleware.AuthMethodPIN (no se importa para evitar el ciclo).
	authMethodPIN = "pin"
)

// HasPermission indica si la lista de permisos del JWT contiene perm.
func HasPermission(perms []string, perm string) bool {
	for _, p := range perms {
		if p == perm {
			return true
		}
	}
	return false
}

// PermissionCatalogReady: el tenant ya tiene sembrado branches.view_all (V176). Mientras no lo tenga (deploy del
// backend antes de migrate-fleet) se mantiene la regla anterior —rol "Administrador"— para no dejar a nadie sin acceso.
func PermissionCatalogReady(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	return database.CachedSchemaProbe(db, "perm:"+PermViewAll, func() bool {
		var n int64
		db.Model(&database.TenantPermission{}).Where("module = ? AND action = ?", "branches", "view_all").Count(&n)
		return n > 0
	})
}

// ResolveViewAll decide si el usuario ve todas las sucursales. Se basa en el permiso branches.view_all, no en el
// nombre del rol ni en cuántas sucursales tiene asignadas. El POS por PIN nunca recibe acceso global.
func ResolveViewAll(db *gorm.DB, perms []string, roleName, authMethod string) bool {
	if authMethod == authMethodPIN {
		return false
	}
	if PermissionCatalogReady(db) {
		return HasPermission(perms, PermViewAll)
	}
	return IsTenantAdmin(roleName) // tenant sin migrar (V176)
}

// RoleHasBranchPermission consulta en BD si el rol tiene branches.<action> (para handlers que no cuentan con el
// JWT del usuario sobre el que operan, p. ej. validar el rol que se asigna a otro usuario).
func RoleHasBranchPermission(db *gorm.DB, roleID uint, action string) bool {
	if db == nil || roleID == 0 {
		return false
	}
	var n int64
	db.Table("tenant_role_permissions").
		Joins("JOIN tenant_permissions ON tenant_permissions.id = tenant_role_permissions.permission_id").
		Where("tenant_role_permissions.role_id = ? AND tenant_permissions.module = ? AND tenant_permissions.action = ?", roleID, "branches", action).
		Count(&n)
	return n > 0
}

// RoleViewsAll: variante por BD de ResolveViewAll para handlers de sesión (login, cambio de sucursal).
func RoleViewsAll(db *gorm.DB, roleID uint, roleName, authMethod string) bool {
	if authMethod == authMethodPIN {
		return false
	}
	if PermissionCatalogReady(db) {
		return RoleHasBranchPermission(db, roleID, "view_all")
	}
	return IsTenantAdmin(roleName)
}

// ---- BranchContext ----

// Modos de acceso a datos de sucursal.
const (
	ModeSingleBranch = "SINGLE_BRANCH"
	ModeAllBranches  = "ALL_BRANCHES" // solo lectura / consulta consolidada
)

// CodeBranchReadOnly: se intentó escribir con "Todas las sucursales" activo.
const CodeBranchReadOnly = "BRANCH_READ_ONLY"

// HeaderBranchMode permite pedir explícitamente el modo ALL_BRANCHES (solo si el usuario tiene branches.view_all).
const HeaderBranchMode = "X-Branch-Mode"

// Context es el contexto de sucursal resuelto por el middleware para la solicitud.
type Context struct {
	UserID           uint
	ActiveBranchID   uint
	AllowedBranchIDs []uint
	CanViewAll       bool
	CanSwitch        bool
	Mode             string
}

// FromCtx devuelve el contexto de sucursal de la solicitud (nil si el middleware no corrió).
func FromCtx(c fiber.Ctx) *Context {
	v, _ := c.Locals("branch_ctx").(*Context)
	return v
}

// IsAllBranchesMode: la solicitud pidió explícitamente "Todas las sucursales" (y tiene permiso para ello).
func IsAllBranchesMode(c fiber.Ctx) bool {
	ctx := FromCtx(c)
	return ctx != nil && ctx.Mode == ModeAllBranches && ctx.CanViewAll
}

type viewAllKey struct {
	pool   interface{}
	roleID uint
}

type viewAllEntry struct {
	val bool
	exp time.Time
}

var viewAllCache sync.Map

// viewAllTTL: los cambios de permisos de un rol se reflejan en "ver todas" en segundos, sin esperar a que venza el JWT.
const viewAllTTL = 10 * time.Second

// RoleViewsAllCached es RoleViewsAll con una caché corta por pool+rol: el middleware la usa en cada solicitud. La decisión
// sale de la BD (no de los permisos del JWT): así coincide con el `can_view_all` que ven los clientes y un rol al que se le
// quita branches.view_all lo pierde de inmediato, no tras 24 h.
func RoleViewsAllCached(db *gorm.DB, roleID uint, roleName, authMethod string) bool {
	if authMethod == authMethodPIN {
		return false
	}
	if db == nil || db.Config == nil {
		return RoleViewsAll(db, roleID, roleName, authMethod)
	}
	key := viewAllKey{pool: db.Config.ConnPool, roleID: roleID}
	if v, ok := viewAllCache.Load(key); ok {
		if e := v.(viewAllEntry); time.Now().Before(e.exp) {
			return e.val
		}
	}
	val := RoleViewsAll(db, roleID, roleName, authMethod)
	viewAllCache.Store(key, viewAllEntry{val: val, exp: time.Now().Add(viewAllTTL)})
	return val
}
