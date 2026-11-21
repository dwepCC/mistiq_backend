package branch

import (
	"errors"

	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Nota: ActiveBranchID usa fiber.Ctx en helpers HTTP.

const (
	CodeSessionUpdated  = "SESSION_UPDATED"
	CodeBranchRequired  = "BRANCH_REQUIRED"
	CodeBranchForbidden = "BRANCH_FORBIDDEN"
)

// IsTenantAdmin indica si el rol puede cambiar sucursal y operar cross-branch en reportes.
func IsTenantAdmin(roleName string) bool {
	return roleName == "Administrador"
}

// ResolveHomeBranchID obtiene la sucursal base del usuario (home > branch_id legacy > principal).
func ResolveHomeBranchID(db *gorm.DB, u *database.TenantUser) (uint, error) {
	if u.HomeBranchID != nil && *u.HomeBranchID > 0 {
		return *u.HomeBranchID, nil
	}
	if u.BranchID != nil && *u.BranchID > 0 {
		return *u.BranchID, nil
	}
	main, err := GetMainBranchID(db)
	if err != nil {
		return 0, err
	}
	return main, nil
}

// GetMainBranchID retorna la sucursal principal activa o la primera activa.
func GetMainBranchID(db *gorm.DB) (uint, error) {
	var b database.TenantBranch
	if err := db.Where("is_main = ? AND active = ?", true, true).First(&b).Error; err == nil {
		return b.ID, nil
	}
	if err := db.Where("active = ?", true).Order("id ASC").First(&b).Error; err != nil {
		return 0, errors.New("no hay sucursal activa configurada")
	}
	return b.ID, nil
}

// BranchBrief datos mínimos de sucursal para API.
type BranchBrief struct {
	ID     uint   `json:"id"`
	Name   string `json:"name"`
	IsMain bool   `json:"is_main"`
}

// GetBranchBrief carga id y nombre de sucursal.
func GetBranchBrief(db *gorm.DB, id uint) (*BranchBrief, error) {
	if id == 0 {
		return nil, errors.New("sucursal inválida")
	}
	var b database.TenantBranch
	if err := db.First(&b, id).Error; err != nil {
		return nil, err
	}
	return &BranchBrief{ID: b.ID, Name: b.Name, IsMain: b.IsMain}, nil
}

// CanSwitchBranch según rol administrador tenant.
func CanSwitchBranch(roleName string, u *database.TenantUser) bool {
	if u != nil && u.CanSwitchBranch {
		return true
	}
	return IsTenantAdmin(roleName)
}

// ActiveBranchID desde Locals (inyectado por middleware).
func ActiveBranchID(c fiber.Ctx) uint {
	v, _ := c.Locals("active_branch_id").(uint)
	return v
}

// IsBranchAdmin: el usuario puede VER todas las sucursales del tenant (hoy: rol Administrador; la Fase 3B lo
// reemplaza por el permiso branches.view_all). Tener varias sucursales asignadas NO lo activa.
func IsBranchAdmin(c fiber.Ctx) bool {
	v, _ := c.Locals("is_branch_admin").(bool)
	return v
}

// AllowedBranchIDs sucursales que el usuario tiene asignadas (más la activa), según el middleware.
func AllowedBranchIDs(c fiber.Ctx) []uint {
	v, _ := c.Locals("allowed_branch_ids").([]uint)
	return v
}

// NoBranchFilter es un id que no existe: un filtro "branch_id = NoBranchFilter" no devuelve nada. Se usa para que
// una consulta sin sucursal resuelta falle cerrada en lugar de mostrar todas.
const NoBranchFilter uint = 1<<32 - 1

// CanAccessBranch indica si el usuario puede LEER datos de la sucursal id (activa, asignada, o todas si ve todo).
func CanAccessBranch(c fiber.Ctx, id uint) bool {
	if id == 0 {
		return false
	}
	if id == ActiveBranchID(c) || IsBranchAdmin(c) {
		return true
	}
	for _, a := range AllowedBranchIDs(c) {
		if a == id {
			return true
		}
	}
	return false
}

// RecordReadable: puede leer un registro cuyo branch_id es recordBranchID. Un registro sin sucursal (0, datos
// previos al multi-sucursal) se considera del tenant y es legible.
func RecordReadable(c fiber.Ctx, recordBranchID uint) bool {
	return recordBranchID == 0 || CanAccessBranch(c, recordBranchID)
}

// RecordWritable: solo se puede modificar/anular/emitir un registro de la sucursal ACTIVA (o sin sucursal).
// Para operar en otra, el usuario debe cambiar de sucursal; ALL_BRANCHES nunca escribe.
func RecordWritable(c fiber.Ctx, recordBranchID uint) bool {
	if recordBranchID == 0 {
		return true
	}
	active := ActiveBranchID(c)
	return active != 0 && recordBranchID == active
}

// ResolveWriteBranchID devuelve la sucursal en la que se registra una operación: SIEMPRE la activa del contexto
// validado (JWT + BD). El branch_id que envíe el cliente solo se acepta si coincide con ella; ni siquiera quien ve
// todas las sucursales puede escribir en otra por el body: debe cambiar de sucursal activa primero.
func ResolveWriteBranchID(c fiber.Ctx, requested uint) (uint, error) {
	active := ActiveBranchID(c)
	if active == 0 {
		return 0, errors.New("sucursal activa requerida")
	}
	if requested == 0 || requested == active {
		return active, nil
	}
	return 0, errors.New("la operación debe registrarse en la sucursal activa; cambie de sucursal para operar en otra")
}

// ResolveReadBranchFilter para listados: 0 = activa; se acepta otra solo si el usuario puede acceder a ella.
// Nunca devuelve 0 ("todas"): sin sucursal resuelta devuelve NoBranchFilter (fail-closed).
func ResolveReadBranchFilter(c fiber.Ctx, requested uint) uint {
	active := ActiveBranchID(c)
	if requested != 0 && CanAccessBranch(c, requested) {
		return requested
	}
	// Modo explícito ALL_BRANCHES (solo lectura, solo con branches.view_all): consulta consolidada.
	if requested == 0 && IsAllBranchesMode(c) {
		return 0
	}
	if active == 0 {
		return NoBranchFilter
	}
	return active
}

// ResolveReportBranchFilter sucursal para dashboards y reportes de ventas, donde "Todas las
// sucursales" es una opción real: solo quien ve todo (IsBranchAdmin) que no elige ninguna (requested
// = 0) y pide "todas" (allWhenEmpty) obtiene 0 = toda la empresa. Una sucursal pedida debe ser accesible.
//
// ResolveReadBranchFilter no sirve para esto: con requested = 0 devuelve la sucursal activa, así
// que un reporte con el selector en "Todas" mostraba solo la sucursal activa mientras el
// dashboard (sin ese filtro) sumaba todas, y los totales de ambos no coincidían.
func ResolveReportBranchFilter(c fiber.Ctx, requested uint, allWhenEmpty bool) uint {
	if requested > 0 && CanAccessBranch(c, requested) {
		return requested
	}
	if requested == 0 && allWhenEmpty && IsBranchAdmin(c) {
		return 0
	}
	if ActiveBranchID(c) == 0 {
		return NoBranchFilter
	}
	return ActiveBranchID(c)
}

// BumpSessionVersion incrementa la versión de sesión del usuario (tenant_users.branch_session_version): revoca
// los access tokens ya emitidos (409 SESSION_UPDATED). Se llama al cambiar sucursales asignadas, rol, permisos
// del rol, contraseña, PIN y al activar/desactivar. Un usuario aún en 0 (sin backfill V178) pasa directo a 2
// para que ni siquiera un token legado con claim 0 sobreviva a la primera revocación.
// No-op si el tenant aún no tiene la columna (rolling deploy / legacy).
func BumpSessionVersion(db *gorm.DB, userID uint) error {
	if !database.TenantBranchSessionVersionReady(db) {
		return nil
	}
	return db.Model(&database.TenantUser{}).Where("id = ?", userID).
		UpdateColumn("branch_session_version", gorm.Expr(bumpExpr)).Error
}

const bumpExpr = "CASE WHEN branch_session_version < 1 THEN 2 ELSE branch_session_version + 1 END"

// BumpSessionVersionForRole revoca las sesiones de TODOS los usuarios de un rol (cambio de permisos del rol).
// No-op si el tenant aún no tiene la columna.
func BumpSessionVersionForRole(db *gorm.DB, roleID uint) error {
	if !database.TenantBranchSessionVersionReady(db) {
		return nil
	}
	return db.Model(&database.TenantUser{}).Where("role_id = ?", roleID).
		UpdateColumn("branch_session_version", gorm.Expr(bumpExpr)).Error
}

// SyncUserBranchFields al crear/actualizar usuario desde branch_id legacy.
func SyncUserBranchFields(u *database.TenantUser, roleName string) {
	if u.HomeBranchID == nil && u.BranchID != nil && *u.BranchID > 0 {
		hb := *u.BranchID
		u.HomeBranchID = &hb
	}
	u.CanSwitchBranch = IsTenantAdmin(roleName)
}
