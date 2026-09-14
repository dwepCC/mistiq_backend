package database

// Seed del RBAC del panel central (SARole/SAPermission/SARolePermission).
//
// Estrategia de idempotencia (aprobada explícitamente antes de implementar):
//   - Permisos: se hace upsert por (module, action). Si el permiso ya existe, no se toca (no se
//     duplica, no se sobreescribe su Label). Si el catálogo de código agrega un permiso nuevo en
//     un deploy futuro, se inserta solo el faltante. Nunca se eliminan permisos automáticamente
//     — un permiso que deja de estar en este archivo simplemente deja de asignarse a roles nuevos,
//     pero sus filas y relaciones existentes en producción no se tocan (evita romper asignaciones
//     manuales ya hechas desde la futura pantalla de roles).
//   - Roles por defecto (Admin, Soporte, Finanzas): se crean solo si NO existen (match por Name).
//     Sus permisos iniciales se asignan ÚNICAMENTE en el momento de la creación. Si el rol ya
//     existe (ya sea porque el seed corrió antes, o porque un superadmin editó sus permisos desde
//     la UI), este seed NO vuelve a tocar su lista de permisos — así no se pisan personalizaciones
//     de producción en cada redeploy.
//
// "Superadmin" NO se modela como fila en sa_roles: es puramente el bypass total vía
// SuperAdminUser.Role == "superadmin" (comparación exacta), independiente de este sistema de
// permisos granulares. Ver comentario en migrations.go junto a SARole/SAPermission.

import "gorm.io/gorm"

// saPermissionDef describe una entrada del catálogo de permisos central.
type saPermissionDef struct {
	Module string
	Action string
	Label  string
}

// SACentralPermissionCatalog es la fuente de verdad del catálogo de permisos del panel central.
// Construido a partir del inventario real de rutas /api/superadmin/* (Fase 0) y las decisiones
// aprobadas sobre qué queda fuera del sistema granular:
//   - "empresas.destroy" NO está en este catálogo: el borrado completo de un tenant queda
//     reservado exclusivamente al bypass de superadmin (nunca otorgable vía rol).
//   - No existe un permiso para rotar la operations-key (PUT /saas-settings/operations-key):
//     ese endpoint queda reservado exclusivamente a superadmin real, fuera de este catálogo.
//   - "system.manage_settings" y "system.impersonate" no se crean (empresas.master_access ya
//     cubre el acceso maestro; no hay evidencia en el código de necesitar los otros dos).
var SACentralPermissionCatalog = []saPermissionDef{
	{Module: "dashboard", Action: "view", Label: "Ver dashboard del panel central"},

	{Module: "empresas", Action: "view", Label: "Ver empresas (tenants)"},
	{Module: "empresas", Action: "create", Label: "Crear empresas"},
	{Module: "empresas", Action: "update", Label: "Editar empresas"},
	{Module: "empresas", Action: "change_status", Label: "Bloquear/desbloquear empresas"},
	{Module: "empresas", Action: "master_access", Label: "Acceso maestro al ERP de una empresa"},

	{Module: "facturador", Action: "view", Label: "Ver configuración de facturador/SUNAT/PSE"},
	{Module: "facturador", Action: "manage", Label: "Editar configuración de facturador/SUNAT/PSE"},
	{Module: "facturador", Action: "sync", Label: "Sincronizar empresa con el facturador"},

	{Module: "migraciones", Action: "view", Label: "Ver estado de migraciones de la flota"},
	{Module: "migraciones", Action: "run", Label: "Ejecutar migración/drift-scan"},
	{Module: "migraciones", Action: "pause", Label: "Pausar migración de un tenant"},
	{Module: "migraciones", Action: "resume", Label: "Reanudar migración de un tenant o de la flota"},
	{Module: "migraciones", Action: "repair", Label: "Reparar migraciones (individual o masivo)"},
	{Module: "migraciones", Action: "backfill", Label: "Ejecutar backfills (individual o masivo)"},

	{Module: "planes", Action: "view", Label: "Ver planes y módulos del catálogo"},
	{Module: "planes", Action: "create", Label: "Crear planes/módulos"},
	{Module: "planes", Action: "update", Label: "Editar planes/módulos"},
	{Module: "planes", Action: "change_status", Label: "Activar/desactivar planes/módulos"},
	{Module: "planes", Action: "destroy", Label: "Eliminar planes/módulos"},

	{Module: "suscripciones", Action: "view", Label: "Ver suscripciones y cobros"},
	{Module: "suscripciones", Action: "create", Label: "Crear suscripciones/cobros manuales"},
	{Module: "suscripciones", Action: "update", Label: "Editar suscripciones (incl. ajustar vigencia)"},
	{Module: "suscripciones", Action: "change_status", Label: "Suspender/reactivar/cancelar suscripciones"},

	{Module: "pagos", Action: "view", Label: "Ver pagos"},
	{Module: "pagos", Action: "approve", Label: "Aprobar pagos"},
	{Module: "pagos", Action: "reject", Label: "Rechazar pagos"},
	{Module: "pagos", Action: "refund", Label: "Revertir pagos ya aprobados"},

	{Module: "documentos", Action: "view", Label: "Ver paquetes de documentos y compras"},
	{Module: "documentos", Action: "manage", Label: "Gestionar catálogo de paquetes de documentos"},
	{Module: "documentos", Action: "approve_purchase", Label: "Aprobar/rechazar compras de paquetes de documentos"},

	{Module: "fiscal", Action: "view", Label: "Ver operaciones fiscales/SUNAT"},
	{Module: "fiscal", Action: "retry", Label: "Reintentar/reenviar un comprobante a SUNAT"},
	{Module: "fiscal", Action: "bulk", Label: "Acciones masivas sobre comprobantes fiscales"},
	{Module: "fiscal", Action: "cancel", Label: "Anular un comprobante fiscal"},

	{Module: "usuarios_central", Action: "view", Label: "Ver usuarios del panel central"},
	{Module: "usuarios_central", Action: "create", Label: "Crear usuarios del panel central"},
	{Module: "usuarios_central", Action: "update", Label: "Editar usuarios del panel central"},
	{Module: "usuarios_central", Action: "reset_password", Label: "Restablecer contraseña de un usuario"},
	{Module: "usuarios_central", Action: "change_role", Label: "Cambiar el rol de un usuario"},
	{Module: "usuarios_central", Action: "change_status", Label: "Activar/desactivar usuarios"},
	{Module: "usuarios_central", Action: "destroy", Label: "Eliminar usuarios del panel central"},

	{Module: "roles", Action: "view", Label: "Ver roles y permisos"},
	{Module: "roles", Action: "create", Label: "Crear roles"},
	{Module: "roles", Action: "update", Label: "Editar roles"},
	{Module: "roles", Action: "delete", Label: "Eliminar roles"},
	{Module: "roles", Action: "manage", Label: "Gestionar roles y su matriz de permisos"},

	{Module: "ajustes", Action: "view", Label: "Ver ajustes del sistema"},
	{Module: "ajustes", Action: "manage", Label: "Editar ajustes del sistema"},

	{Module: "system", Action: "view_audit", Label: "Ver bitácora de auditoría (pantalla futura)"},
}

// saDefaultRoleDef describe un rol inicial y sus permisos ("module.action") al momento de crearse.
type saDefaultRoleDef struct {
	Name        string
	Description string
	Permissions []string
}

// SADefaultRoles son los roles iniciales aprobados. IsSystem=true en todos: no deben poder
// eliminarse desde la futura pantalla de roles (protección contra dejar usuarios sin acceso).
var SADefaultRoles = []saDefaultRoleDef{
	{
		Name:        "Admin",
		Description: "Acceso amplio al panel central. No puede administrar roles ni cambiar el rol de otros usuarios (reservado a superadmin).",
		Permissions: []string{
			"dashboard.view",
			"empresas.view", "empresas.create", "empresas.update", "empresas.change_status", "empresas.master_access",
			"facturador.view", "facturador.manage", "facturador.sync",
			"migraciones.view", "migraciones.run", "migraciones.pause", "migraciones.resume", "migraciones.repair", "migraciones.backfill",
			"planes.view", "planes.create", "planes.update", "planes.change_status", "planes.destroy",
			"suscripciones.view", "suscripciones.create", "suscripciones.update", "suscripciones.change_status",
			"pagos.view", "pagos.approve", "pagos.reject", "pagos.refund",
			"documentos.view", "documentos.manage", "documentos.approve_purchase",
			"fiscal.view", "fiscal.retry", "fiscal.bulk", "fiscal.cancel",
			"usuarios_central.view", "usuarios_central.create", "usuarios_central.update",
			"usuarios_central.reset_password", "usuarios_central.change_status", "usuarios_central.destroy",
			// NO incluye: usuarios_central.change_role, roles.* (administración de roles/escalamiento
			// reservada a superadmin real), empresas.destroy (no existe como permiso, ver catálogo).
			"roles.view",
			"ajustes.view", "ajustes.manage",
		},
	},
	{
		Name:        "Soporte",
		Description: "Consulta y diagnóstico: solo lectura sobre empresas, fiscal y migraciones.",
		Permissions: []string{
			"dashboard.view",
			"empresas.view",
			"fiscal.view",
			"migraciones.view",
		},
	},
	{
		Name:        "Finanzas",
		Description: "Gestión de pagos, suscripciones y paquetes de documentos. Sin acceso a migraciones ni administración de usuarios.",
		Permissions: []string{
			"dashboard.view",
			"pagos.view", "pagos.approve", "pagos.reject", "pagos.refund",
			"suscripciones.view", "suscripciones.update",
			"planes.view",
			"documentos.view", "documentos.approve_purchase",
		},
	},
}

// SASeedRolesAndPermissions siembra el catálogo de permisos y los roles iniciales del panel
// central. Idempotente: seguro de ejecutar en cada arranque/deploy (ver estrategia arriba).
func SASeedRolesAndPermissions(db *gorm.DB) error {
	if err := saSeedPermissions(db); err != nil {
		return err
	}
	return saSeedDefaultRoles(db)
}

// saSeedPermissions inserta los permisos del catálogo que aún no existan (match por module+action).
// Nunca actualiza ni elimina permisos existentes.
func saSeedPermissions(db *gorm.DB) error {
	for _, def := range SACentralPermissionCatalog {
		var existing SAPermission
		err := db.Where("module = ? AND action = ?", def.Module, def.Action).First(&existing).Error
		if err == nil {
			continue // ya existe, no se toca
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		perm := SAPermission{Module: def.Module, Action: def.Action, Label: def.Label}
		if err := db.Create(&perm).Error; err != nil {
			return err
		}
	}
	return nil
}

// saSeedDefaultRoles crea los roles iniciales si no existen. Los permisos iniciales solo se
// asignan en el momento de creación del rol — si el rol ya existe, no se tocan sus permisos.
func saSeedDefaultRoles(db *gorm.DB) error {
	for _, def := range SADefaultRoles {
		var role SARole
		err := db.Where("name = ?", def.Name).First(&role).Error
		if err == nil {
			continue // el rol ya existe: no se recrea ni se reasignan permisos
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}

		role = SARole{Name: def.Name, Description: def.Description, IsSystem: true}
		if err := db.Create(&role).Error; err != nil {
			return err
		}

		permIDs, err := saPermissionIDsForKeys(db, def.Permissions)
		if err != nil {
			return err
		}
		for _, pid := range permIDs {
			if err := db.Create(&SARolePermission{RoleID: role.ID, PermissionID: pid}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// saPermissionIDsForKeys resuelve claves "module.action" a IDs de SAPermission ya sembrados.
func saPermissionIDsForKeys(db *gorm.DB, keys []string) ([]uint, error) {
	ids := make([]uint, 0, len(keys))
	for _, key := range keys {
		module, action, ok := splitModuleActionKey(key)
		if !ok {
			continue
		}
		var perm SAPermission
		if err := db.Where("module = ? AND action = ?", module, action).First(&perm).Error; err != nil {
			return nil, err
		}
		ids = append(ids, perm.ID)
	}
	return ids, nil
}

func splitModuleActionKey(key string) (module, action string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == '.' {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}
