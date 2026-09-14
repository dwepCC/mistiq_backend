package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V132PermissionCatalogRedesign es el rediseño completo del catálogo de permisos del tenant:
// cada vista/acción real de Tukifac pasa a tener su propio permiso en vez de compartir uno
// genérico de un módulo vecino (Cotizaciones/CxC compartían sales.*, CxP compartía purchases.*,
// Facturación tenía un solo billing.send para enviar/anular/notas/guías/retenciones, Inventario un
// solo inventory.manage para crear/confirmar/anular/transferir). También cierra 3 huecos reales
// que no exigían NINGÚN permiso de rol: anular venta (sales.cancel, ya estaba en el catálogo pero
// nunca se validaba), aplicar devoluciones pendientes (mismo permiso) y Suscripción completa
// (subscription.view/manage — cualquier cajero podía comprar paquetes de documentos SUNAT).
//
// Esta migración hace DOS cosas por cada tenant:
//  1. Crea (si faltan) las filas de catálogo nuevas — mismo patrón que V131 (FirstOrCreate con
//     Where(struct), nunca Where(sql crudo): con SQL crudo GORM no copia Module/Action al crear).
//  2. Retro-concede permisos para que el deploy sea transparente:
//     - Los 4 permisos que antes NO exigían nada (sales.cancel, cashbank.arqueo,
//     subscription.view, subscription.manage) se conceden a TODOS los roles existentes.
//     - Los permisos que reemplazan a uno "prestado" de otro módulo se conceden ESPEJANDO
//     exactamente qué rol tenía el permiso viejo — no a todos los roles — para no ensanchar
//     accesos de más: quien tenía sales.view sigue viendo Cotizaciones/CxC, quien tenía
//     sales.create sigue pudiendo crear/editar/eliminar/convertir cotizaciones y cobrar/
//     confirmar CxC, quien tenía purchases.view/create sigue viendo/pagando CxP, quien tenía
//     billing.send sigue pudiendo anular con NC, emitir ND, guías y documentos avanzados,
//     quien tenía ecommerce.view o ecommerce.manage sigue gestionando pedidos web.
//     - inventory.{create_document,confirm_document,void_document,transfer,confirm_transfer,
//     cancel_transfer,adjust,import_adjustment} NO necesitan backfill: "inventory.manage" ya
//     las implica automáticamente vía la regla genérica "{modulo}.manage" en
//     pkg/middleware/tenant_permissions.go — quien ya tenía inventory.manage sigue pudiendo
//     hacer todo, sin conceder filas nuevas.
type V132PermissionCatalogRedesign struct{}

func (V132PermissionCatalogRedesign) Version() int { return 132 }
func (V132PermissionCatalogRedesign) Name() string {
	return "permission_catalog_redesign"
}

// v132NewCatalog catálogo nuevo a crear si falta — mismos module/action/label que
// internal/users/service/role_service.go (SeedPermissions).
var v132NewCatalog = []database.TenantPermission{
	{Module: "inventory", Action: "create_document", Label: "Crear documento de ingreso/egreso"},
	{Module: "inventory", Action: "confirm_document", Label: "Confirmar documento de inventario"},
	{Module: "inventory", Action: "void_document", Label: "Anular documento de inventario"},
	{Module: "inventory", Action: "transfer", Label: "Transferir entre sucursales"},
	{Module: "inventory", Action: "confirm_transfer", Label: "Confirmar transferencia recibida"},
	{Module: "inventory", Action: "cancel_transfer", Label: "Cancelar/revertir transferencia"},
	{Module: "inventory", Action: "adjust", Label: "Ajustar stock"},
	{Module: "inventory", Action: "import_adjustment", Label: "Ajuste de stock por importación"},
	{Module: "quotations", Action: "view", Label: "Ver cotizaciones"},
	{Module: "quotations", Action: "create", Label: "Crear cotizaciones"},
	{Module: "quotations", Action: "edit", Label: "Editar cotizaciones"},
	{Module: "quotations", Action: "delete", Label: "Eliminar cotizaciones"},
	{Module: "quotations", Action: "convert", Label: "Convertir cotización en venta"},
	{Module: "receivables", Action: "view", Label: "Ver cuentas por cobrar"},
	{Module: "receivables", Action: "collect", Label: "Registrar cobro (CxC)"},
	{Module: "receivables", Action: "confirm_bn", Label: "Confirmar depósito Banco de la Nación"},
	{Module: "payables", Action: "view", Label: "Ver cuentas por pagar"},
	{Module: "payables", Action: "pay", Label: "Registrar pago (CxP)"},
	{Module: "cashbank", Action: "arqueo", Label: "Registrar arqueo de caja"},
	{Module: "billing", Action: "manage", Label: "Gestionar facturación electrónica (todo lo de abajo)"},
	{Module: "billing", Action: "credit_note", Label: "Anular venta con nota de crédito"},
	{Module: "billing", Action: "debit_note", Label: "Emitir nota de débito"},
	{Module: "billing", Action: "despatch", Label: "Emitir guías de remisión"},
	{Module: "billing", Action: "advanced_docs", Label: "Retenciones, percepciones y reversiones"},
	{Module: "ecommerce", Action: "orders", Label: "Gestionar pedidos web"},
	{Module: "subscription", Action: "view", Label: "Ver suscripción y facturación de Tukifac"},
	{Module: "subscription", Action: "manage", Label: "Registrar pagos y comprar paquetes de documentos"},
}

// v132BlanketGrants permisos que antes no exigía NINGÚN endpoint — se conceden a todos los roles.
var v132BlanketGrants = [][2]string{
	{"sales", "cancel"},
	{"cashbank", "arqueo"},
	{"subscription", "view"},
	{"subscription", "manage"},
}

// v132Mirror nuevo permiso → permiso viejo cuya presencia en el rol determina si se concede el
// nuevo (espejo exacto del acceso anterior, no un blanket).
var v132Mirror = []struct {
	newModule, newAction string
	oldModule, oldAction string
}{
	{"quotations", "view", "sales", "view"},
	{"quotations", "create", "sales", "create"},
	{"quotations", "edit", "sales", "create"},
	{"quotations", "delete", "sales", "create"},
	{"quotations", "convert", "sales", "create"},
	{"receivables", "view", "sales", "view"},
	{"receivables", "collect", "sales", "create"},
	{"receivables", "confirm_bn", "sales", "create"},
	{"payables", "view", "purchases", "view"},
	{"payables", "pay", "purchases", "create"},
	{"billing", "credit_note", "billing", "send"},
	{"billing", "debit_note", "billing", "send"},
	{"billing", "despatch", "billing", "send"},
	{"billing", "advanced_docs", "billing", "send"},
}

// v132MirrorAny nuevo permiso → CUALQUIERA de varios permisos viejos concede el nuevo.
var v132MirrorAny = []struct {
	newModule, newAction string
	oldPerms             [][2]string
}{
	{"ecommerce", "orders", [][2]string{{"ecommerce", "view"}, {"ecommerce", "manage"}}},
}

func v132EnsurePermission(db *gorm.DB, module, action, label string) (uint, error) {
	var perm database.TenantPermission
	err := db.Where(database.TenantPermission{Module: module, Action: action}).
		Attrs(database.TenantPermission{Label: label}).
		FirstOrCreate(&perm).Error
	return perm.ID, err
}

func v132FindPermissionID(db *gorm.DB, module, action string) (uint, bool, error) {
	var perm database.TenantPermission
	err := db.Where("module = ? AND action = ?", module, action).First(&perm).Error
	if err != nil {
		if gorm.ErrRecordNotFound == err {
			return 0, false, nil
		}
		return 0, false, err
	}
	return perm.ID, true, nil
}

func v132RoleHasPermission(db *gorm.DB, roleID, permID uint) (bool, error) {
	var count int64
	err := db.Model(&database.TenantRolePermission{}).
		Where("role_id = ? AND permission_id = ?", roleID, permID).
		Count(&count).Error
	return count > 0, err
}

func v132GrantIfMissing(db *gorm.DB, roleID, permID uint) error {
	has, err := v132RoleHasPermission(db, roleID, permID)
	if err != nil || has {
		return err
	}
	return db.Create(&database.TenantRolePermission{RoleID: roleID, PermissionID: permID}).Error
}

func (V132PermissionCatalogRedesign) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) {
		return nil
	}

	// 1. Catálogo: crear lo que falte.
	newIDs := make(map[string]uint, len(v132NewCatalog))
	for _, want := range v132NewCatalog {
		id, err := v132EnsurePermission(db, want.Module, want.Action, want.Label)
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

	// 2. Blanket: permisos que antes nadie necesitaba — todos los roles los reciben.
	// sales.cancel ya existía en el catálogo (desde antes de este rediseño, solo nunca se
	// validaba en el backend) mientras que cashbank.arqueo/subscription.* son nuevos — por eso
	// se asegura(crea) aquí en vez de solo buscar en v132NewCatalog.
	for _, ma := range v132BlanketGrants {
		id, err := v132EnsurePermission(db, ma[0], ma[1], ma[0]+"."+ma[1])
		if err != nil {
			return err
		}
		for _, role := range roles {
			if err := v132GrantIfMissing(db, role.ID, id); err != nil {
				return err
			}
		}
	}

	// 3. Espejo exacto: solo los roles que ya tenían el permiso viejo reciben el nuevo.
	oldPermCache := map[string]uint{}
	getOldID := func(module, action string) (uint, bool, error) {
		key := module + "." + action
		if id, ok := oldPermCache[key]; ok {
			return id, true, nil
		}
		id, found, err := v132FindPermissionID(db, module, action)
		if err != nil || !found {
			return 0, found, err
		}
		oldPermCache[key] = id
		return id, true, nil
	}

	for _, m := range v132Mirror {
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
			has, err := v132RoleHasPermission(db, role.ID, oldID)
			if err != nil {
				return err
			}
			if !has {
				continue
			}
			if err := v132GrantIfMissing(db, role.ID, newID); err != nil {
				return err
			}
		}
	}

	for _, m := range v132MirrorAny {
		newID, ok := newIDs[m.newModule+"."+m.newAction]
		if !ok {
			continue
		}
		var oldIDs []uint
		for _, op := range m.oldPerms {
			id, found, err := getOldID(op[0], op[1])
			if err != nil {
				return err
			}
			if found {
				oldIDs = append(oldIDs, id)
			}
		}
		if len(oldIDs) == 0 {
			continue
		}
		for _, role := range roles {
			grant := false
			for _, oldID := range oldIDs {
				has, err := v132RoleHasPermission(db, role.ID, oldID)
				if err != nil {
					return err
				}
				if has {
					grant = true
					break
				}
			}
			if !grant {
				continue
			}
			if err := v132GrantIfMissing(db, role.ID, newID); err != nil {
				return err
			}
		}
	}

	// 4. Limpieza: permisos "fantasma" que nunca tuvieron un endpoint ni pantalla real detrás
	// (sales.edit/sales.delete: no existe "editar"/"eliminar" venta; purchases.edit: no existe
	// "editar" compra; reports.view: ningún reporte lo usa, cada uno depende del permiso de su
	// módulo real) — quedaban como casillas en Roles que no hacían nada. Se borran sus grants
	// antes que la fila de catálogo para no dejar registros huérfanos.
	phantoms := [][2]string{
		{"sales", "edit"}, {"sales", "delete"}, {"purchases", "edit"}, {"reports", "view"},
	}
	for _, ma := range phantoms {
		id, found, err := v132FindPermissionID(db, ma[0], ma[1])
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		if err := db.Where("permission_id = ?", id).Delete(&database.TenantRolePermission{}).Error; err != nil {
			return err
		}
		if err := db.Delete(&database.TenantPermission{}, id).Error; err != nil {
			return err
		}
	}

	return nil
}
