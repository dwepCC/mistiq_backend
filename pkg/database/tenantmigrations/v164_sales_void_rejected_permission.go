package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V164SalesVoidRejectedPermission agrega al catálogo el permiso sales.void_rejected (anular
// localmente una factura/boleta que SUNAT rechazó: revierte caja, stock y anticipos).
//
// A diferencia de V155 (blanket grant a todos los roles), aquí solo se asigna a los roles llamados
// "Administrador": la acción mueve dinero y stock, así que el resto de roles lo reciben solo si el
// tenant se lo da explícitamente desde Roles. Para tenants nuevos, RoleService.SeedPermissions ya
// incluye el permiso en el catálogo y Administrador recibe el catálogo completo.
type V164SalesVoidRejectedPermission struct{}

func (V164SalesVoidRejectedPermission) Version() int { return 164 }
func (V164SalesVoidRejectedPermission) Name() string  { return "sales_void_rejected_permission" }

func (V164SalesVoidRejectedPermission) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) {
		return nil
	}

	var perm database.TenantPermission
	if err := db.Where(database.TenantPermission{Module: "sales", Action: "void_rejected"}).
		Attrs(database.TenantPermission{Label: "Anular localmente un comprobante rechazado por SUNAT"}).
		FirstOrCreate(&perm).Error; err != nil {
		return err
	}

	var roles []database.TenantRole
	if err := db.Where("name = ?", "Administrador").Find(&roles).Error; err != nil {
		return err
	}
	for _, role := range roles {
		var count int64
		if err := db.Model(&database.TenantRolePermission{}).
			Where("role_id = ? AND permission_id = ?", role.ID, perm.ID).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if err := db.Create(&database.TenantRolePermission{RoleID: role.ID, PermissionID: perm.ID}).Error; err != nil {
			return err
		}
	}
	return nil
}
