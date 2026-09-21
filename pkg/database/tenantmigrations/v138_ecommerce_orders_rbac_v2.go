package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V138EcommerceOrdersRBACV2 reemplaza el permiso único e indiferenciado "ecommerce.orders" por 6
// permisos granulares (Contrato ecommerce v2 §7, docs/ECOMMERCE-EVOLUTION-CONTRACT.md), necesarios
// porque "ecommerce.orders" no permite distinguir "solo preparar" de "puede despachar/convertir/
// devolver" — la separación de responsabilidades que pide el contrato es imposible de lograr con
// el catálogo anterior.
//
// "ecommerce.orders" NO se retira: queda deprecado en el catálogo (compatibilidad para tenants
// existentes), simplemente deja de ofrecerse en internal/users/service/role_service.go
// (SeedPermissions) para tenants NUEVOS a partir de este deploy.
//
// Backfill: todo rol que ya tenía "ecommerce.orders" (o, transitivamente, "ecommerce.view"/
// "ecommerce.manage" — la migración V132 ya los había espejado hacia "ecommerce.orders") recibe el
// superset view+manage+convert+dispatch — el mismo alcance indiferenciado que ya tenía, para que
// el deploy sea transparente y ningún tenant pierda funcionalidad de un día para otro.
// "ecommerce.orders_return" NO se incluye en este backfill a propósito: el punto 4 del usuario
// restringe DEVUELTO a Administrador/Supervisor, y conceder "return" solo porque alguien tenía el
// permiso viejo indiferenciado violaría esa restricción explícita. Administrador ya lo tiene vía
// "ecommerce.manage" (implica todo el módulo); Supervisor lo recibe por su nuevo default en
// internal/users/service/role_service.go (defaultRolePermissions), no por este backfill.
type V138EcommerceOrdersRBACV2 struct{}

func (V138EcommerceOrdersRBACV2) Version() int { return 138 }
func (V138EcommerceOrdersRBACV2) Name() string { return "ecommerce_orders_rbac_v2" }

var v138NewCatalog = []database.TenantPermission{
	{Module: "ecommerce", Action: "orders_view", Label: "Ver pedidos web"},
	{Module: "ecommerce", Action: "orders_prepare", Label: "Preparar pedidos web (picking/empaquetado)"},
	{Module: "ecommerce", Action: "orders_manage", Label: "Confirmar, cancelar y gestionar pedidos web"},
	{Module: "ecommerce", Action: "orders_convert", Label: "Convertir pedidos web a venta"},
	{Module: "ecommerce", Action: "orders_dispatch", Label: "Despachar pedidos web"},
	{Module: "ecommerce", Action: "orders_return", Label: "Registrar devoluciones de pedidos web"},
}

// v138BackfillActions permisos que reciben el backfill de compatibilidad — deliberadamente sin
// "orders_return", ver comentario del struct.
var v138BackfillActions = []string{"orders_view", "orders_manage", "orders_convert", "orders_dispatch"}

func (V138EcommerceOrdersRBACV2) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) {
		return nil
	}

	newIDs := make(map[string]uint, len(v138NewCatalog))
	for _, want := range v138NewCatalog {
		id, err := v132EnsurePermission(db, want.Module, want.Action, want.Label)
		if err != nil {
			return err
		}
		newIDs[want.Module+"."+want.Action] = id
	}

	oldID, found, err := v132FindPermissionID(db, "ecommerce", "orders")
	if err != nil {
		return err
	}
	if !found {
		return nil // catálogo viejo tampoco existe en este tenant (provisioning en curso)
	}

	var roles []database.TenantRole
	if err := db.Find(&roles).Error; err != nil {
		return err
	}
	for _, role := range roles {
		has, err := v132RoleHasPermission(db, role.ID, oldID)
		if err != nil {
			return err
		}
		if !has {
			continue
		}
		for _, action := range v138BackfillActions {
			newID, ok := newIDs["ecommerce."+action]
			if !ok {
				continue
			}
			if err := v132GrantIfMissing(db, role.ID, newID); err != nil {
				return err
			}
		}
	}
	return nil
}
