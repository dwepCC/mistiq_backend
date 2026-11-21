package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V155SalesOverridePricePermission agrega al catálogo el permiso sales.override_price: quien lo
// tenga queda exento de validateAuthorizedPrices al vender (reactivada en el mismo release que
// esta migración). Ver backend_go/docs/INCIDENT-2026-09-17-PRICE-AUTHORIZATION.md — la validación
// se desactivó por completo el 2026-09-17 porque rompía el precio editable legítimo del POS/
// "Registrar venta" sin ningún mecanismo de excepción; este permiso es esa excepción.
//
// Blanket grant a TODOS los roles existentes del tenant (decisión explícita del usuario, mismo
// criterio que V132PermissionCatalogRedesign usó para sales.cancel/cashbank.arqueo/subscription.*,
// permisos que antes no exigían nada): el día del deploy nadie pierde la capacidad de editar el
// precio al vender —hoy no hay ninguna restricción— así el negocio real de cada tenant sigue
// funcionando exactamente igual. Cada tenant decide DESPUÉS, desde Roles, a qué roles quitarle el
// permiso si quiere restringir la edición de precio a solo algunos (ej. dejarlo solo en
// Administrador/Supervisor y quitárselo a Cajero).
//
// Para tenants NUEVOS (creados después de este release): RoleService.SeedPermissions ya incluye
// este permiso en el catálogo, y defaultRolePermissions ya lo asigna a Cajero/Vendedor por
// defecto (mismo criterio "sin restricción inicial" que esta migración aplica a los existentes;
// Administrador recibe el catálogo completo aparte, como todos los demás permisos).
type V155SalesOverridePricePermission struct{}

func (V155SalesOverridePricePermission) Version() int { return 155 }
func (V155SalesOverridePricePermission) Name() string  { return "sales_override_price_permission" }

func (V155SalesOverridePricePermission) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) {
		return nil
	}

	var perm database.TenantPermission
	if err := db.Where(database.TenantPermission{Module: "sales", Action: "override_price"}).
		Attrs(database.TenantPermission{Label: "Cambiar precio al vender sin restricción de catálogo"}).
		FirstOrCreate(&perm).Error; err != nil {
		return err
	}

	var roles []database.TenantRole
	if err := db.Find(&roles).Error; err != nil {
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
