package tenantmigrations

import (
	"errors"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V183SplitContactPermissions separa los permisos de contactos en clientes y proveedores.
//
// Clientes y proveedores son una misma tabla (tenant_contacts.type) y compartían
// contacts.{view,create,edit,delete}: un rol que veía clientes veía también proveedores. Ahora hay
// customers.* y suppliers.* (mismas cuatro acciones) para poder dar un rol solo a uno de los dos.
//
// Cada rol que tenía contacts.<acción> recibe customers.<acción> Y suppliers.<acción>, de modo que
// nadie gana ni pierde acceso el día del deploy; después el dueño ajusta desde Roles. Con los roles
// ya convertidos se retiran los contacts.* del catálogo (y sus asignaciones), para que Roles no
// muestre un permiso que ya no hace nada. Idempotente: una segunda corrida no encuentra contacts.*.
//
// Catálogo espejo: internal/users/service/role_service.go (SeedPermissions).
type V183SplitContactPermissions struct{}

func (V183SplitContactPermissions) Version() int { return 183 }
func (V183SplitContactPermissions) Name() string { return "split_contact_permissions" }

var v183Actions = []string{"view", "create", "edit", "delete"}

var v183Labels = map[string]string{
	"customers.view":   "Ver clientes",
	"customers.create": "Crear clientes",
	"customers.edit":   "Editar clientes",
	"customers.delete": "Eliminar clientes",
	"suppliers.view":   "Ver proveedores",
	"suppliers.create": "Crear proveedores",
	"suppliers.edit":   "Editar proveedores",
	"suppliers.delete": "Eliminar proveedores",
}

func (V183SplitContactPermissions) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) ||
		!db.Migrator().HasTable(&database.TenantRolePermission{}) {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, action := range v183Actions {
			var legacy database.TenantPermission
			err := tx.Where("module = ? AND action = ?", "contacts", action).First(&legacy).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			legacyExists := err == nil

			var roleIDs []uint
			if legacyExists {
				if err := tx.Model(&database.TenantRolePermission{}).
					Where("permission_id = ?", legacy.ID).Pluck("role_id", &roleIDs).Error; err != nil {
					return err
				}
			}

			for _, module := range []string{"customers", "suppliers"} {
				var perm database.TenantPermission
				if err := tx.Where(database.TenantPermission{Module: module, Action: action}).
					Attrs(database.TenantPermission{Label: v183Labels[module+"."+action]}).
					FirstOrCreate(&perm).Error; err != nil {
					return err
				}
				for _, roleID := range roleIDs {
					if err := grantIfMissing(tx, roleID, perm.ID); err != nil {
						return err
					}
				}
			}

			if legacyExists {
				if err := tx.Where("permission_id = ?", legacy.ID).Delete(&database.TenantRolePermission{}).Error; err != nil {
					return err
				}
				if err := tx.Delete(&database.TenantPermission{}, legacy.ID).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
