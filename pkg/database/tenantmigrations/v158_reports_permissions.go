package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V158ReportsPermissions le da a la sección Reportes sus propios permisos (reports.*), en vez de
// depender del .view del módulo dueño de los datos como hasta ahora (sales.view, products.view,
// purchases.view, inventory.view, cashbank.view — así estaban gateadas las 7 páginas de
// /reports/* en frontend_tenant/src/components/routing/AppRouter.tsx). Antes hubo un permiso
// "reports.view" que se borró en V132 por no estar atado a ningún endpoint ni pantalla real; esta
// vez cada permiso nuevo SÍ reemplaza el gate real de una página concreta, para no repetir ese
// error.
//
// Solo aplica al panel tenant (Tukifac) — Tukichef no tiene sección de Reportes y se autoriza con
// un sistema de permisos propio (pkg/restaurantperm), sin relación con TenantPermission.
//
// El backend NO gana un endpoint nuevo que revisar: los reportes siguen leyendo los mismos
// endpoints de siempre (GET /sales, /sales/by-product, /purchases, etc.), que se quedan exigiendo
// el permiso de su módulo real — reports.* solo decide si la PÁGINA del reporte se muestra en el
// frontend. Por eso el backfill de abajo espeja exactamente el acceso actual (quien ya podía ver
// un reporte porque tenía el .view de su módulo, sigue viéndolo el día del deploy); a partir de
// ahí cada rol se puede ajustar por separado desde Roles sin tocar el permiso del módulo.
type V158ReportsPermissions struct{}

func (V158ReportsPermissions) Version() int { return 158 }
func (V158ReportsPermissions) Name() string { return "reports_permissions" }

// v158Catalog permisos nuevos — mismos module/action/label que
// internal/users/service/role_service.go (SeedPermissions).
var v158Catalog = []database.TenantPermission{
	{Module: "reports", Action: "manage", Label: "Ver todos los reportes"},
	{Module: "reports", Action: "sales", Label: "Ver reporte de ventas"},
	{Module: "reports", Action: "sales_by_product", Label: "Ver reporte de ventas por producto"},
	{Module: "reports", Action: "notes", Label: "Ver reporte de notas de crédito/débito"},
	{Module: "reports", Action: "products", Label: "Ver reporte de productos"},
	{Module: "reports", Action: "purchases", Label: "Ver reporte de compras"},
	{Module: "reports", Action: "kardex", Label: "Ver reporte de kardex"},
	{Module: "reports", Action: "cash", Label: "Ver reporte de caja"},
}

// v158Mirror nuevo permiso → permiso viejo cuya presencia en el rol determina si se concede el
// nuevo (espejo exacto del acceso que ya tenía por depender del .view del módulo dueño).
var v158Mirror = []struct {
	newModule, newAction string
	oldModule, oldAction string
}{
	{"reports", "sales", "sales", "view"},
	{"reports", "sales_by_product", "sales", "view"},
	{"reports", "notes", "sales", "view"},
	{"reports", "products", "products", "view"},
	{"reports", "purchases", "purchases", "view"},
	{"reports", "kardex", "inventory", "view"},
	{"reports", "cash", "cashbank", "view"},
}

func v158EnsurePermission(db *gorm.DB, module, action, label string) (uint, error) {
	var perm database.TenantPermission
	// Where(struct) — no Where(sql, args...) — para que FirstOrCreate copie Module/Action al
	// crear el registro cuando no existe (mismo cuidado que V131/V132/V155).
	err := db.Where(database.TenantPermission{Module: module, Action: action}).
		Attrs(database.TenantPermission{Label: label}).
		FirstOrCreate(&perm).Error
	return perm.ID, err
}

func v158FindPermissionID(db *gorm.DB, module, action string) (uint, bool, error) {
	var perm database.TenantPermission
	err := db.Where("module = ? AND action = ?", module, action).First(&perm).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return 0, false, nil
		}
		return 0, false, err
	}
	return perm.ID, true, nil
}

func v158RoleHasPermission(db *gorm.DB, roleID, permID uint) (bool, error) {
	var count int64
	err := db.Model(&database.TenantRolePermission{}).
		Where("role_id = ? AND permission_id = ?", roleID, permID).
		Count(&count).Error
	return count > 0, err
}

func v158GrantIfMissing(db *gorm.DB, roleID, permID uint) error {
	has, err := v158RoleHasPermission(db, roleID, permID)
	if err != nil || has {
		return err
	}
	return db.Create(&database.TenantRolePermission{RoleID: roleID, PermissionID: permID}).Error
}

func (V158ReportsPermissions) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) {
		return nil
	}

	// 1. Catálogo: crear lo que falte.
	newIDs := make(map[string]uint, len(v158Catalog))
	for _, want := range v158Catalog {
		id, err := v158EnsurePermission(db, want.Module, want.Action, want.Label)
		if err != nil {
			return err
		}
		newIDs[want.Module+"."+want.Action] = id
	}

	var roles []database.TenantRole
	if err := db.Find(&roles).Error; err != nil {
		return err
	}
	if len(roles) == 0 {
		return nil
	}

	// 2. Espejo: cada rol recibe reports.<x> solo si ya tenía el .view del módulo dueño de ese
	// reporte — preserva exactamente quién ve qué reporte hoy, sin ensanchar accesos de más.
	// reports.manage queda sin backfill a propósito: no reemplaza ningún gate existente, es un
	// atajo nuevo ("todo lo de abajo", vía la regla genérica {modulo}.manage en
	// pkg/middleware/tenant_permissions.go) que cada tenant asigna manualmente si lo quiere.
	oldPermCache := map[string]uint{}
	getOldID := func(module, action string) (uint, bool, error) {
		key := module + "." + action
		if id, ok := oldPermCache[key]; ok {
			return id, true, nil
		}
		id, found, err := v158FindPermissionID(db, module, action)
		if err != nil || !found {
			return 0, found, err
		}
		oldPermCache[key] = id
		return id, true, nil
	}

	for _, m := range v158Mirror {
		newID, ok := newIDs[m.newModule+"."+m.newAction]
		if !ok {
			continue
		}
		oldID, found, err := getOldID(m.oldModule, m.oldAction)
		if err != nil {
			return err
		}
		if !found {
			continue // catálogo viejo tampoco existe en este tenant (provisioning en curso)
		}
		for _, role := range roles {
			has, err := v158RoleHasPermission(db, role.ID, oldID)
			if err != nil {
				return err
			}
			if !has {
				continue
			}
			if err := v158GrantIfMissing(db, role.ID, newID); err != nil {
				return err
			}
		}
	}

	return nil
}
