package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V131BackfillModulesEcommerceFleetPermissions concede a todos los roles existentes los permisos
// modules.manage, ecommerce.{view,manage} y fleet.{view,manage}.
//
// internal/modules, internal/ecommerce e internal/fleet no exigían ningún permiso de rol (modules
// usaba el rol hardcodeado "Administrador"; ecommerce y fleet solo el módulo del plan). El catálogo
// ahora sí los define y las rutas ya los exigen (ver internal/{modules,ecommerce,fleet}/routes.go).
// Mismo criterio que V130: sin este backfill, el deploy le quitaría acceso a cualquier rol que hoy
// usa esas pantallas con normalidad — nunca se validó el permiso, así que casi ningún rol lo tenía
// marcado aunque lo necesitara.
//
// IMPORTANTE (bug encontrado probando esta migración en local, 2026-09-11): estos 3 módulos son
// entradas NUEVAS del catálogo (RoleService.SeedPermissions), pero esa función solo inserta el
// catálogo completo si la tabla tenant_permissions está totalmente vacía (`if count > 0 { return
// nil }`) — o sea, nunca se ejecuta de nuevo para un tenant que ya tenía permisos (prácticamente
// toda la flota). Un primer intento de esta migración que solo buscaba el permiso y, si no
// existía, lo saltaba ("ya lo completará SeedPermissions") no concedía nada en ningún tenant
// existente porque SeedPermissions nunca vuelve a correr para ellos. Por eso este migration
// crea la fila de catálogo si falta (FirstOrCreate) en vez de solo buscarla.
type V131BackfillModulesEcommerceFleetPermissions struct{}

func (V131BackfillModulesEcommerceFleetPermissions) Version() int { return 131 }
func (V131BackfillModulesEcommerceFleetPermissions) Name() string {
	return "backfill_modules_ecommerce_fleet_permissions"
}

// v131PermissionCatalog permisos recién exigidos que se crean (si faltan) y se retro-conceden a
// todos los roles existentes. Mismos module/action/label que RoleService.SeedPermissions.
var v131PermissionCatalog = []database.TenantPermission{
	{Module: "modules", Action: "manage", Label: "Activar/desactivar módulos"},
	{Module: "ecommerce", Action: "view", Label: "Ver tienda virtual y pedidos web"},
	{Module: "ecommerce", Action: "manage", Label: "Configurar tienda virtual"},
	{Module: "fleet", Action: "view", Label: "Ver transportistas, conductores y vehículos"},
	{Module: "fleet", Action: "manage", Label: "Gestionar transportistas, conductores y vehículos"},
}

// v131ModuleActions solo module/action, para los tests que verifican conteos.
var v131ModuleActions = [][2]string{
	{"modules", "manage"},
	{"ecommerce", "view"}, {"ecommerce", "manage"},
	{"fleet", "view"}, {"fleet", "manage"},
}

func (V131BackfillModulesEcommerceFleetPermissions) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) {
		return nil
	}

	permIDs := make([]uint, 0, len(v131PermissionCatalog))
	for _, want := range v131PermissionCatalog {
		var perm database.TenantPermission
		// Where(struct) — no Where(sql, args...) — para que FirstOrCreate copie Module/Action al
		// crear el registro cuando no existe (con la condición en SQL crudo los deja en blanco).
		err := db.Where(database.TenantPermission{Module: want.Module, Action: want.Action}).
			Attrs(database.TenantPermission{Label: want.Label}).
			FirstOrCreate(&perm).Error
		if err != nil {
			return err
		}
		permIDs = append(permIDs, perm.ID)
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
