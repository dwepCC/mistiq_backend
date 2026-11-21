package service

import (
	"errors"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type RoleService struct {
	db *gorm.DB
}

func NewRoleService(db *gorm.DB) *RoleService {
	return &RoleService{db: db}
}

func (s *RoleService) List() ([]database.TenantRole, error) {
	var roles []database.TenantRole
	err := s.db.Order("name ASC").Find(&roles).Error
	return roles, err
}

func (s *RoleService) GetByID(id uint) (*database.TenantRole, error) {
	var role database.TenantRole
	if err := s.db.First(&role, id).Error; err != nil {
		return nil, err
	}
	return &role, nil
}

func (s *RoleService) Create(name, description string) (*database.TenantRole, error) {
	if name == "" {
		return nil, errors.New("el nombre del rol es requerido")
	}
	var existing database.TenantRole
	if err := s.db.Where("name = ?", name).First(&existing).Error; err == nil {
		return nil, errors.New("ya existe un rol con ese nombre")
	}
	role := &database.TenantRole{Name: name, Description: description}
	err := s.db.Create(role).Error
	return role, err
}

func (s *RoleService) Update(id uint, name, description string) error {
	return s.db.Model(&database.TenantRole{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":        name,
		"description": description,
	}).Error
}

func (s *RoleService) Delete(id uint) error {
	var role database.TenantRole
	if err := s.db.First(&role, id).Error; err != nil {
		return err
	}
	if role.IsSystem {
		return errors.New("no se puede eliminar un rol del sistema")
	}
	return s.db.Delete(&role).Error
}

// AllPermissions retorna todos los permisos disponibles.
func (s *RoleService) AllPermissions() ([]database.TenantPermission, error) {
	var perms []database.TenantPermission
	err := s.db.Order("module ASC, action ASC").Find(&perms).Error
	return perms, err
}

// RolePermissions retorna los IDs de permisos asignados a un rol.
func (s *RoleService) RolePermissions(roleID uint) ([]uint, error) {
	var rps []database.TenantRolePermission
	if err := s.db.Where("role_id = ?", roleID).Find(&rps).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, len(rps))
	for i, rp := range rps {
		ids[i] = rp.PermissionID
	}
	return ids, nil
}

// SetRolePermissions reemplaza todos los permisos de un rol.
//
// Revoca las sesiones de todos los usuarios del rol (branch_session_version + 1): los permisos viajan en el JWT.
func (s *RoleService) SetRolePermissions(roleID uint, permissionIDs []uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := branch.BumpSessionVersionForRole(tx, roleID); err != nil {
			return err
		}
		if err := tx.Where("role_id = ?", roleID).Delete(&database.TenantRolePermission{}).Error; err != nil {
			return err
		}
		for _, pid := range permissionIDs {
			if err := tx.Create(&database.TenantRolePermission{
				RoleID: roleID, PermissionID: pid,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// GetRolePermissionKeys devuelve los permisos del rol en formato "module.action" para el JWT.
func (s *RoleService) GetRolePermissionKeys(roleID uint) ([]string, error) {
	var perms []database.TenantPermission
	err := s.db.Table("tenant_permissions").
		Select("tenant_permissions.module, tenant_permissions.action").
		Joins("INNER JOIN tenant_role_permissions ON tenant_role_permissions.permission_id = tenant_permissions.id").
		Where("tenant_role_permissions.role_id = ?", roleID).
		Find(&perms).Error
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(perms))
	for i, p := range perms {
		keys[i] = p.Module + "." + p.Action
	}
	return keys, nil
}

// SeedPermissions asegura que exista cada permiso del catálogo vigente — idempotente, seguro de
// llamar cualquier cantidad de veces y en cualquier momento.
//
// ANTES: la función entera se saltaba con un único "if count > 0 { return nil }" — pensada para
// solo sembrar el catálogo completo la PRIMERA vez que se llama, en un tenant recién creado con
// tenant_permissions totalmente vacía. Eso se rompía en cuanto CUALQUIER otra cosa insertaba una
// fila en tenant_permissions antes de esta llamada: migraciones de schema como
// V131BackfillModulesEcommerceFleetPermissions y V132PermissionCatalogRedesign corren para todo
// tenant (incluido uno recién creado) DURANTE RunTenantSchemaMigrations — es decir, antes de que
// ProvisionTenantSeed cree los roles y antes de esta misma llamada en TenantService.CreateTenant
// — y ambas usan FirstOrCreate para adelantar sus propias filas de catálogo nuevas
// incondicionalmente. Para un tenant nuevo, eso dejaba tenant_permissions con esas pocas filas
// "nuevas" (32 de 69) ANTES de que esta función corriera — su guarda veía count=32 (no 0) y
// abortaba sin sembrar los 37 permisos "base" (dashboard, users, roles, company, contacts,
// products, sales, purchases, cashbank básico, billing.send, memberships) — el Administrador de
// ese tenant quedaba con solo esos 32, sin poder hacer casi nada. Encontrado en producción
// 2026-09-12 (RUC 20615266010, tenant "inversioneshermanas", y "castillo" con el mismo síntoma).
//
// AHORA: se recorre el catálogo completo y se asegura cada fila con FirstOrCreate (mismo patrón
// ya usado en V131/V132/V130) — sin importar qué tan poblada esté la tabla al momento de llamar.
func (s *RoleService) SeedPermissions() error {
	perms := []database.TenantPermission{
		{Module: "dashboard", Action: "view", Label: "Ver dashboard"},
		{Module: "users", Action: "view", Label: "Ver usuarios"},
		{Module: "users", Action: "create", Label: "Crear usuarios"},
		{Module: "users", Action: "edit", Label: "Editar usuarios"},
		{Module: "users", Action: "delete", Label: "Eliminar usuarios"},
		{Module: "roles", Action: "view", Label: "Ver roles"},
		{Module: "roles", Action: "manage", Label: "Gestionar roles"},
		{Module: "company", Action: "view", Label: "Ver configuración de empresa"},
		{Module: "company", Action: "edit", Label: "Editar configuración de empresa"},
		{Module: "customers", Action: "view", Label: "Ver clientes"},
		{Module: "customers", Action: "create", Label: "Crear clientes"},
		{Module: "customers", Action: "edit", Label: "Editar clientes"},
		{Module: "customers", Action: "delete", Label: "Eliminar clientes"},
		{Module: "suppliers", Action: "view", Label: "Ver proveedores"},
		{Module: "suppliers", Action: "create", Label: "Crear proveedores"},
		{Module: "suppliers", Action: "edit", Label: "Editar proveedores"},
		{Module: "suppliers", Action: "delete", Label: "Eliminar proveedores"},
		{Module: "products", Action: "view", Label: "Ver productos"},
		{Module: "products", Action: "create", Label: "Crear productos"},
		{Module: "products", Action: "edit", Label: "Editar productos"},
		{Module: "products", Action: "delete", Label: "Eliminar productos"},
		{Module: "products", Action: "view_cost", Label: "Ver y editar el costo de los productos"},
		{Module: "pos", Action: "view_stock", Label: "Ver stock por sucursal en el punto de venta"},
		{Module: "pos", Action: "view_cost", Label: "Ver costo del producto en el punto de venta"},
		{Module: "pos", Action: "view_prices", Label: "Ver precios del producto en el punto de venta"},
		{Module: "inventory", Action: "view", Label: "Ver inventario"},
		{Module: "inventory", Action: "manage", Label: "Gestionar inventario (todo lo de abajo)"},
		{Module: "inventory", Action: "create_document", Label: "Crear documento de ingreso/egreso"},
		{Module: "inventory", Action: "confirm_document", Label: "Confirmar documento de inventario"},
		{Module: "inventory", Action: "void_document", Label: "Anular documento de inventario"},
		{Module: "inventory", Action: "transfer", Label: "Transferir entre sucursales"},
		{Module: "inventory", Action: "confirm_transfer", Label: "Confirmar transferencia recibida"},
		{Module: "inventory", Action: "cancel_transfer", Label: "Cancelar/revertir transferencia"},
		{Module: "inventory", Action: "adjust", Label: "Ajustar stock"},
		{Module: "inventory", Action: "import_adjustment", Label: "Ajuste de stock por importación"},
		{Module: "sales", Action: "view", Label: "Ver ventas"},
		{Module: "sales", Action: "create", Label: "Crear ventas"},
		{Module: "sales", Action: "cancel", Label: "Anular ventas"},
		{Module: "sales", Action: "pos", Label: "Usar punto de venta"},
		{Module: "sales", Action: "void_rejected", Label: "Anular localmente un comprobante rechazado por SUNAT"},
		{Module: "sales", Action: "override_price", Label: "Cambiar precio al vender sin restricción de catálogo"},
		{Module: "quotations", Action: "view", Label: "Ver cotizaciones"},
		{Module: "quotations", Action: "create", Label: "Crear cotizaciones"},
		{Module: "quotations", Action: "edit", Label: "Editar cotizaciones"},
		{Module: "quotations", Action: "delete", Label: "Eliminar cotizaciones"},
		{Module: "quotations", Action: "convert", Label: "Convertir cotización en venta"},
		{Module: "receivables", Action: "view", Label: "Ver cuentas por cobrar"},
		{Module: "receivables", Action: "collect", Label: "Registrar cobro (CxC)"},
		{Module: "receivables", Action: "confirm_bn", Label: "Confirmar depósito Banco de la Nación"},
		{Module: "purchases", Action: "view", Label: "Ver compras"},
		{Module: "purchases", Action: "create", Label: "Crear compras"},
		{Module: "purchases", Action: "delete", Label: "Anular compras"},
		{Module: "payables", Action: "view", Label: "Ver cuentas por pagar"},
		{Module: "payables", Action: "pay", Label: "Registrar pago (CxP)"},
		{Module: "cashbank", Action: "view", Label: "Ver caja y bancos"},
		{Module: "cashbank", Action: "manage", Label: "Gestionar caja y bancos (todo lo de abajo)"},
		{Module: "cashbank", Action: "open", Label: "Abrir caja"},
		{Module: "cashbank", Action: "close", Label: "Cerrar caja"},
		{Module: "cashbank", Action: "movements", Label: "Movimientos de caja"},
		{Module: "cashbank", Action: "arqueo", Label: "Registrar arqueo de caja"},
		// Permisos finos de Caja y bancos (V173): cada pantalla/acción se asigna por separado.
		{Module: "cashbank", Action: "income_view", Label: "Ver ingresos de caja"},
		{Module: "cashbank", Action: "expense_view", Label: "Ver egresos de caja"},
		{Module: "cashbank", Action: "bank_view", Label: "Ver cuentas y bancos"},
		{Module: "cashbank", Action: "bank_manage", Label: "Crear/editar cuentas bancarias y registrar movimientos bancarios"},
		{Module: "cashbank", Action: "report_view", Label: "Ver reporte de caja"},
		{Module: "cashbank", Action: "payment_methods_manage", Label: "Gestionar métodos de pago"},
		{Module: "cashbank", Action: "delete_session", Label: "Eliminar una caja sin movimientos"},
		{Module: "billing", Action: "manage", Label: "Gestionar facturación electrónica (todo lo de abajo)"},
		{Module: "billing", Action: "view", Label: "Ver consulta de comprobantes (solo lectura)"},
		{Module: "billing", Action: "send", Label: "Enviar/consultar comprobantes en SUNAT"},
		{Module: "billing", Action: "credit_note", Label: "Anular venta con nota de crédito"},
		{Module: "billing", Action: "debit_note", Label: "Emitir nota de débito"},
		{Module: "billing", Action: "despatch", Label: "Emitir guías de remisión"},
		{Module: "billing", Action: "advanced_docs", Label: "Retenciones, percepciones y reversiones"},
		{Module: "memberships", Action: "view", Label: "Ver membresías"},
		{Module: "memberships", Action: "create", Label: "Crear membresías"},
		{Module: "memberships", Action: "edit", Label: "Editar membresías"},
		{Module: "memberships", Action: "delete", Label: "Eliminar membresías"},
		{Module: "memberships", Action: "generate_sale", Label: "Generar venta desde membresía"},
		{Module: "modules", Action: "manage", Label: "Activar/desactivar módulos"},
		{Module: "ecommerce", Action: "view", Label: "Ver configuración de tienda virtual"},
		{Module: "ecommerce", Action: "manage", Label: "Configurar tienda virtual"},
		// "ecommerce.orders" (permiso único e indiferenciado) queda DEPRECADO: ya no se ofrece acá
		// para tenants nuevos (Contrato ecommerce v2 §7/§17, docs/ECOMMERCE-EVOLUTION-CONTRACT.md).
		// Sigue existiendo en el catálogo de tenants que ya lo tenían de antes (esta lista solo
		// controla qué se siembra para tenants NUEVOS vía FirstOrCreate — no borra filas
		// existentes) y se retiraría, si acaso, en una migración de limpieza independiente y
		// posterior a una auditoría de uso real, no acá.
		{Module: "ecommerce", Action: "orders_view", Label: "Ver pedidos web"},
		{Module: "ecommerce", Action: "orders_prepare", Label: "Preparar pedidos web (picking/empaquetado)"},
		{Module: "ecommerce", Action: "orders_manage", Label: "Confirmar, cancelar y gestionar pedidos web"},
		{Module: "ecommerce", Action: "orders_convert", Label: "Convertir pedidos web a venta"},
		{Module: "ecommerce", Action: "orders_dispatch", Label: "Despachar pedidos web"},
		{Module: "ecommerce", Action: "orders_return", Label: "Registrar devoluciones de pedidos web"},
		{Module: "fleet", Action: "view", Label: "Ver transportistas, conductores y vehículos"},
		{Module: "fleet", Action: "manage", Label: "Gestionar transportistas, conductores y vehículos"},
		{Module: "subscription", Action: "view", Label: "Ver suscripción y facturación de Mistiq"},
		{Module: "subscription", Action: "manage", Label: "Registrar pagos y comprar paquetes de documentos"},
		// reports.*: gatea la PÁGINA de cada reporte en /reports/* (solo Tukifac — Tukichef no
		// tiene esta sección). Los endpoints que consumen esos reportes siguen exigiendo el
		// permiso de su módulo real (sales.view, products.view, etc.), sin cambios.
		// Contabilidad (módulo accounting, V174): cada pantalla/acción se asigna por separado.
		{Module: "accounting", Action: "sales_register", Label: "Contabilidad: exportar registro de ventas"},
		{Module: "accounting", Action: "purchases_register", Label: "Contabilidad: exportar registro de compras"},
		{Module: "accounting", Action: "export_formats", Label: "Contabilidad: exportar formatos a sistemas contables"},
		{Module: "accounting", Action: "summary_report", Label: "Contabilidad: reporte resumido de ventas"},
		{Module: "accounting", Action: "ledger", Label: "Contabilidad: libro mayor"},
		{Module: "accounting", Action: "sire_view", Label: "Contabilidad: consultar SIRE (ventas y compras)"},
		{Module: "accounting", Action: "sire_manage", Label: "Contabilidad: solicitar propuesta SIRE y aceptar propuesta de ventas"},
		{Module: "accounting", Action: "settings", Label: "Contabilidad: cuentas contables y credenciales SIRE"},
		{Module: "products", Action: "manage_global", Label: "Catálogo global: crear/editar/eliminar productos, categorías, marcas, unidades, imágenes, atributos y precio global"},
		{Module: "branches", Action: "view_all", Label: "Sucursales: ver todas las sucursales (consulta consolidada, solo lectura)"},
		{Module: "branches", Action: "switch", Label: "Sucursales: cambiar entre las sucursales asignadas"},
		{Module: "branches", Action: "manage", Label: "Sucursales: crear/editar sucursales y asignar acceso total"},
		{Module: "reports", Action: "manage", Label: "Ver todos los reportes"},
		{Module: "reports", Action: "sales", Label: "Ver reporte de ventas"},
		{Module: "reports", Action: "sales_by_product", Label: "Ver reporte de ventas por producto"},
		{Module: "reports", Action: "notes", Label: "Ver reporte de notas de crédito/débito"},
		{Module: "reports", Action: "products", Label: "Ver reporte de productos"},
		{Module: "reports", Action: "purchases", Label: "Ver reporte de compras"},
		{Module: "reports", Action: "kardex", Label: "Ver reporte de kardex"},
		{Module: "reports", Action: "cash", Label: "Ver reporte de caja"},
		{Module: "reports", Action: "profit", Label: "Ver reporte de utilidades"},
	}

	for _, want := range perms {
		var perm database.TenantPermission
		// Where(struct) — no Where(sql, args...) — para que FirstOrCreate copie Module/Action al
		// crear el registro cuando no existe (con la condición en SQL crudo los deja en blanco;
		// mismo cuidado que V131/V132).
		if err := s.db.Where(database.TenantPermission{Module: want.Module, Action: want.Action}).
			Attrs(database.TenantPermission{Label: want.Label}).
			FirstOrCreate(&perm).Error; err != nil {
			return err
		}
	}
	return nil
}

// defaultRolePermissions punto de partida razonable por rol del sistema — el tenant los puede
// ajustar después desde Roles. Administrador no aparece aquí: siempre recibe el catálogo
// completo (ver TenantService.CreateTenant), nunca un subconjunto.
//
// Antes de esto, seedTenantRoles (pkg/database/tenant_provision_seed.go) creaba estos 5 roles
// SIN ningún permiso — ninguna otra parte del código se los asignaba nunca, así que un tenant
// nuevo que fuera a usar, por ejemplo, el rol "Cajero" tal cual se lo encontraba sin poder hacer
// nada hasta que alguien entrara a Roles a marcarle casillas a mano.
var defaultRolePermissions = map[string][][2]string{
	"Supervisor": {
		{"dashboard", "view"},
		{"roles", "view"},
		{"company", "view"},
		{"customers", "view"}, {"suppliers", "view"},
		{"products", "view"},
		{"pos", "view_stock"}, {"pos", "view_prices"},
		{"inventory", "view"},
		{"sales", "view"},
		{"quotations", "view"},
		{"receivables", "view"},
		{"purchases", "view"},
		{"payables", "view"},
		{"cashbank", "view"}, {"cashbank", "arqueo"},
		{"cashbank", "income_view"}, {"cashbank", "expense_view"}, {"cashbank", "bank_view"}, {"cashbank", "report_view"},
		{"memberships", "view"},
		{"subscription", "view"},
		// Gestión completa de pedidos web (Contrato v2 §7): confirmar, preparar, convertir,
		// despachar y — a diferencia de Vendedor — devolver (DEVUELTO restringido a
		// Administrador/Supervisor, punto 4 aprobado).
		{"ecommerce", "orders_view"}, {"ecommerce", "orders_manage"}, {"ecommerce", "orders_prepare"},
		{"ecommerce", "orders_convert"}, {"ecommerce", "orders_dispatch"}, {"ecommerce", "orders_return"},
		{"reports", "sales"}, {"reports", "sales_by_product"}, {"reports", "notes"},
		{"reports", "products"}, {"reports", "purchases"}, {"reports", "kardex"}, {"reports", "cash"},
		{"reports", "profit"},
	},
	"Cajero": {
		{"dashboard", "view"},
		{"customers", "view"}, {"suppliers", "view"},
		{"products", "view"},
		{"pos", "view_stock"}, {"pos", "view_prices"},
		{"sales", "view"}, {"sales", "create"}, {"sales", "pos"}, {"sales", "override_price"},
		{"billing", "view"},
		{"quotations", "view"}, {"quotations", "convert"},
		{"receivables", "view"}, {"receivables", "collect"},
		{"cashbank", "view"}, {"cashbank", "open"}, {"cashbank", "close"},
		{"cashbank", "movements"}, {"cashbank", "arqueo"},
		{"cashbank", "income_view"}, {"cashbank", "expense_view"}, {"cashbank", "report_view"},
		{"reports", "sales"}, {"reports", "sales_by_product"}, {"reports", "notes"}, {"reports", "products"}, {"reports", "cash"},
		{"reports", "profit"},
	},
	"Vendedor": {
		{"dashboard", "view"},
		{"customers", "view"}, {"customers", "create"}, {"customers", "edit"}, {"suppliers", "view"},
		{"products", "view"},
		{"pos", "view_stock"}, {"pos", "view_prices"},
		{"sales", "view"}, {"sales", "create"}, {"sales", "pos"}, {"sales", "override_price"},
		{"billing", "view"},
		{"quotations", "view"}, {"quotations", "create"}, {"quotations", "edit"}, {"quotations", "convert"},
		{"receivables", "view"}, {"receivables", "collect"},
		{"memberships", "view"}, {"memberships", "create"}, {"memberships", "generate_sale"},
		{"cashbank", "view"},
		// Consultar, confirmar, gestionar y convertir pedidos web — SIN despachar ni devolver
		// (Contrato v2 §7, matriz de roles aprobada).
		{"ecommerce", "orders_view"}, {"ecommerce", "orders_manage"}, {"ecommerce", "orders_convert"},
		{"reports", "sales"}, {"reports", "sales_by_product"}, {"reports", "notes"}, {"reports", "products"}, {"reports", "cash"},
		{"reports", "profit"},
	},
	"Almacenero": {
		{"dashboard", "view"},
		{"customers", "view"}, {"suppliers", "view"},
		{"products", "view"}, {"products", "create"}, {"products", "edit"},
		{"products", "view_cost"}, {"pos", "view_stock"}, {"pos", "view_prices"}, {"pos", "view_cost"},
		{"inventory", "view"}, {"inventory", "manage"},
		{"inventory", "create_document"}, {"inventory", "confirm_document"}, {"inventory", "void_document"},
		{"inventory", "transfer"}, {"inventory", "confirm_transfer"}, {"inventory", "cancel_transfer"},
		{"inventory", "adjust"}, {"inventory", "import_adjustment"},
		{"purchases", "view"}, {"purchases", "create"},
		// Ver y preparar pedidos web — SIN convertir a venta, cancelar, devolver ni modificar
		// información comercial (Contrato v2 §7, punto 3 aprobado explícitamente).
		{"ecommerce", "orders_view"}, {"ecommerce", "orders_prepare"},
		{"reports", "products"}, {"reports", "kardex"}, {"reports", "purchases"},
	},
	"Contador": {
		{"dashboard", "view"},
		{"company", "view"}, {"company", "edit"},
		{"customers", "view"}, {"suppliers", "view"},
		{"sales", "view"},
		{"purchases", "view"},
		{"receivables", "view"}, {"receivables", "collect"}, {"receivables", "confirm_bn"},
		{"payables", "view"}, {"payables", "pay"},
		{"cashbank", "view"}, {"cashbank", "income_view"}, {"cashbank", "expense_view"}, {"cashbank", "bank_view"}, {"cashbank", "report_view"},
		{"billing", "manage"}, {"billing", "view"}, {"billing", "send"}, {"billing", "credit_note"}, {"billing", "debit_note"},
		{"billing", "despatch"}, {"billing", "advanced_docs"},
		{"subscription", "view"}, {"subscription", "manage"},
		{"reports", "sales"}, {"reports", "sales_by_product"}, {"reports", "notes"}, {"reports", "purchases"}, {"reports", "cash"},
		{"reports", "profit"},
	},
}

// SeedDefaultRolePermissions asigna el set por defecto de defaultRolePermissions a cada rol del
// sistema que todavía no tenga NINGÚN permiso asignado — pensada para llamarse una única vez,
// al crear un tenant nuevo, justo después de SeedPermissions (para que los permisos referenciados
// ya existan) y de asignarle el catálogo completo a Administrador.
//
// Deliberadamente nunca toca un rol que YA tenga algo asignado: no es una función de "reparación"
// para tenants existentes (que pueden haber vaciado un rol a propósito, o ya haberlo configurado
// distinto) — solo completa el estado inicial de un tenant recién creado.
func (s *RoleService) SeedDefaultRolePermissions() error {
	var roles []database.TenantRole
	if err := s.db.Where("is_system = ?", true).Find(&roles).Error; err != nil {
		return err
	}
	perms, err := s.AllPermissions()
	if err != nil {
		return err
	}
	idByKey := make(map[string]uint, len(perms))
	for _, p := range perms {
		idByKey[p.Module+"."+p.Action] = p.ID
	}

	for _, role := range roles {
		want, ok := defaultRolePermissions[role.Name]
		if !ok {
			continue // Administrador (recibe todo aparte) o un rol sin default definido
		}
		existing, err := s.RolePermissions(role.ID)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			continue
		}
		var ids []uint
		for _, ma := range want {
			if id, ok := idByKey[ma[0]+"."+ma[1]]; ok {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		if err := s.SetRolePermissions(role.ID, ids); err != nil {
			return err
		}
	}
	return nil
}
