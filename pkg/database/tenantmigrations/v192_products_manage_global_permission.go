package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V192ProductsManageGlobalPermission separa la información GLOBAL del catálogo (datos maestros, alta/baja, categorías,
// marcas, unidades, imágenes, galería, unidades de venta, atributos y precio global) de la información comercial por
// sucursal (precio por sucursal, visibilidad y stock).
//
// Siembra products.manage_global y se la otorga a todo rol que hoy ya puede crear, editar o eliminar productos
// (products.create / edit / delete / manage): nadie pierde ni gana acceso el día del deploy. Después el dueño puede
// retirarla desde Roles a los perfiles que solo deben tocar lo comercial de su sucursal.
//
// Solo agrega filas. Idempotente. Catálogo espejo: role_service.go (SeedPermissions).
type V192ProductsManageGlobalPermission struct{}

func (V192ProductsManageGlobalPermission) Version() int { return 192 }
func (V192ProductsManageGlobalPermission) Name() string  { return "products_manage_global_permission" }

func (V192ProductsManageGlobalPermission) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) ||
		!db.Migrator().HasTable(&database.TenantRolePermission{}) {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var global database.TenantPermission
		if err := tx.Where(database.TenantPermission{Module: "products", Action: "manage_global"}).
			Attrs(database.TenantPermission{Label: "Catálogo global: crear/editar/eliminar productos, categorías, marcas, unidades, imágenes, atributos y precio global"}).
			FirstOrCreate(&global).Error; err != nil {
			return err
		}
		var sources []uint
		if err := tx.Model(&database.TenantPermission{}).
			Where("module = ? AND action IN ?", "products", []string{"create", "edit", "delete", "manage"}).
			Pluck("id", &sources).Error; err != nil {
			return err
		}
		if len(sources) == 0 {
			return nil
		}
		var roleIDs []uint
		if err := tx.Model(&database.TenantRolePermission{}).
			Where("permission_id IN ?", sources).Distinct().Pluck("role_id", &roleIDs).Error; err != nil {
			return err
		}
		for _, rid := range roleIDs {
			if err := grantIfMissing(tx, rid, global.ID); err != nil {
				return err
			}
		}
		return nil
	})
}
