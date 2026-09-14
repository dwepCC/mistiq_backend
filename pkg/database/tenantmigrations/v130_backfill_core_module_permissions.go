package tenantmigrations

import (
	"errors"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V130BackfillCoreModulePermissions concede a todos los roles existentes los permisos de
// contactos, inventario, empresa, envío a SUNAT y dashboard.
//
// Hasta esta versión, las rutas de esos módulos (internal/contacts, internal/inventory,
// internal/company, internal/billing, internal/dashboard) solo exigían autenticación + módulo
// del plan contratado — nunca el rol del usuario, pese a que el catálogo de permisos ya define
// contacts.{view,create,edit,delete}, inventory.{view,manage}, company.{view,edit}, billing.send
// y dashboard.view, y el frontend ya deja marcarlos/desmarcarlos en Roles. Ese hueco es la causa
// de que "los permisos que le dan a sus roles no funcionan": marcar o no esas casillas no tenía
// ningún efecto.
//
// pkg/middleware/permissions.go y las rutas de esos módulos ahora sí validan el permiso. Sin este
// backfill, cualquier rol de un tenant existente que nunca marcó esas casillas (la inmensa
// mayoría, porque marcarlas no cambiaba nada) perdería de golpe acceso a funciones que hoy usa con
// normalidad. Este backfill iguala el punto de partida al comportamiento actual — nadie pierde
// acceso el día del deploy — para que, de ahí en adelante, activar o desactivar estos permisos
// desde Roles sí tenga efecto real.
type V130BackfillCoreModulePermissions struct{}

func (V130BackfillCoreModulePermissions) Version() int { return 130 }
func (V130BackfillCoreModulePermissions) Name() string { return "backfill_core_module_permissions" }

// v130ModuleActions permisos recién exigidos que se retro-conceden a todos los roles existentes.
var v130ModuleActions = [][2]string{
	{"contacts", "view"}, {"contacts", "create"}, {"contacts", "edit"}, {"contacts", "delete"},
	{"inventory", "view"}, {"inventory", "manage"},
	{"company", "view"}, {"company", "edit"},
	{"billing", "send"},
	{"dashboard", "view"},
}

func (V130BackfillCoreModulePermissions) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) {
		return nil
	}

	var permIDs []uint
	for _, ma := range v130ModuleActions {
		var perm database.TenantPermission
		err := db.Where("module = ? AND action = ?", ma[0], ma[1]).First(&perm).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// Catálogo aún no sembrado en este tenant (p. ej. provisioning en curso): nada
				// que retro-conceder todavía, RoleService.SeedPermissions lo completará.
				continue
			}
			return err
		}
		permIDs = append(permIDs, perm.ID)
	}
	if len(permIDs) == 0 {
		return nil
	}

	var roles []database.TenantRole
	if err := db.Find(&roles).Error; err != nil {
		return err
	}

	for _, role := range roles {
		var already []uint
		if err := db.Model(&database.TenantRolePermission{}).
			Where("role_id = ? AND permission_id IN ?", role.ID, permIDs).
			Pluck("permission_id", &already).Error; err != nil {
			return err
		}
		have := make(map[uint]struct{}, len(already))
		for _, id := range already {
			have[id] = struct{}{}
		}
		for _, pid := range permIDs {
			if _, ok := have[pid]; ok {
				continue
			}
			if err := db.Create(&database.TenantRolePermission{RoleID: role.ID, PermissionID: pid}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
