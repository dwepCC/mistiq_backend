package cmd

import (
	"flag"
	"fmt"
	"sort"

	"tukifac/pkg/database"
)

// tenantPermissionCatalog espeja el catálogo actual de internal/users/service/role_service.go
// (RoleService.SeedPermissions) — usado solo para DIAGNOSTICAR qué le falta a un tenant frente al
// catálogo vigente en el código desplegado, nunca para escribir nada.
var tenantPermissionCatalog = [][2]string{
	{"dashboard", "view"},
	{"users", "view"}, {"users", "create"}, {"users", "edit"}, {"users", "delete"},
	{"roles", "view"}, {"roles", "manage"},
	{"company", "view"}, {"company", "edit"},
	{"contacts", "view"}, {"contacts", "create"}, {"contacts", "edit"}, {"contacts", "delete"},
	{"products", "view"}, {"products", "create"}, {"products", "edit"}, {"products", "delete"},
	{"inventory", "view"}, {"inventory", "manage"}, {"inventory", "create_document"},
	{"inventory", "confirm_document"}, {"inventory", "void_document"}, {"inventory", "transfer"},
	{"inventory", "confirm_transfer"}, {"inventory", "cancel_transfer"}, {"inventory", "adjust"},
	{"inventory", "import_adjustment"},
	{"sales", "view"}, {"sales", "create"}, {"sales", "cancel"}, {"sales", "pos"},
	{"quotations", "view"}, {"quotations", "create"}, {"quotations", "edit"}, {"quotations", "delete"},
	{"quotations", "convert"},
	{"receivables", "view"}, {"receivables", "collect"}, {"receivables", "confirm_bn"},
	{"purchases", "view"}, {"purchases", "create"}, {"purchases", "delete"},
	{"payables", "view"}, {"payables", "pay"},
	{"cashbank", "view"}, {"cashbank", "manage"}, {"cashbank", "open"}, {"cashbank", "close"},
	{"cashbank", "movements"}, {"cashbank", "arqueo"},
	{"billing", "manage"}, {"billing", "send"}, {"billing", "credit_note"}, {"billing", "debit_note"},
	{"billing", "despatch"}, {"billing", "advanced_docs"},
	{"memberships", "view"}, {"memberships", "create"}, {"memberships", "edit"}, {"memberships", "delete"},
	{"memberships", "generate_sale"},
	{"modules", "manage"},
	{"ecommerce", "view"}, {"ecommerce", "manage"}, {"ecommerce", "orders"},
	{"fleet", "view"}, {"fleet", "manage"},
	{"subscription", "view"}, {"subscription", "manage"},
}

// RunDiagnoseTenantPermissions: solo lectura. Dado un RUC, ubica el tenant, y para cada rol
// marcado is_system (típicamente "Administrador") compara el catálogo de permisos vigente en el
// código desplegado contra: (a) lo que existe en tenant_permissions de ESE tenant (si falta ahí,
// es un backfill/migración de catálogo pendiente) y (b) lo que ese rol tiene realmente asignado
// en tenant_role_permissions (si existe en el catálogo pero no está asignado al rol, es un tema
// de datos del rol, no de migración).
func RunDiagnoseTenantPermissions(args []string) int {
	fs := flag.NewFlagSet("diagnose-tenant-permissions", flag.ExitOnError)
	ruc := fs.String("ruc", "", "RUC del tenant (obligatorio)")
	slug := fs.String("slug", "", "alternativa: slug del tenant")
	roleName := fs.String("role", "", "filtrar por nombre de rol exacto (por defecto: todos los is_system)")
	_ = fs.Parse(args)

	if *ruc == "" && *slug == "" {
		fmt.Println("uso: diagnose-tenant-permissions --ruc=<ruc> | --slug=<slug> [--role=<nombre>]")
		return 1
	}

	var t database.Tenant
	q := database.CentralDB
	if *ruc != "" {
		q = q.Where("ruc = ?", *ruc)
	} else {
		q = q.Where("slug = ?", *slug)
	}
	if err := q.First(&t).Error; err != nil {
		fmt.Printf("✗ tenant no encontrado (ruc=%q slug=%q): %v\n", *ruc, *slug, err)
		return 1
	}
	fmt.Printf("tenant: slug=%s name=%q ruc=%s status=%s plan=%s\n", t.Slug, t.Name, t.RUC, t.Status, t.Plan)

	db, err := database.OpenTenantDBForMigration(t.DBName)
	if err != nil {
		fmt.Printf("✗ abrir conexión tenant: %v\n", err)
		return 1
	}
	defer database.CloseTenantDB(db)

	// Estado de schema: versión probada por historial vs target del código desplegado.
	var historyCount int64
	db.Model(&database.TenantMigrationHistory{}).
		Where("type = ? AND success = ?", database.MigrationHistoryTypeSchema, true).
		Count(&historyCount)
	fmt.Printf("historial de schema: %d migraciones con éxito registradas\n", historyCount)

	// Catálogo real en este tenant.
	var existingPerms []database.TenantPermission
	if err := db.Find(&existingPerms).Error; err != nil {
		fmt.Printf("✗ leer tenant_permissions: %v\n", err)
		return 1
	}
	existing := make(map[[2]string]uint, len(existingPerms))
	for _, p := range existingPerms {
		existing[[2]string{p.Module, p.Action}] = p.ID
	}

	fmt.Printf("catálogo en este tenant: %d permisos (código vigente define %d)\n",
		len(existingPerms), len(tenantPermissionCatalog))

	var missingFromCatalog [][2]string
	for _, want := range tenantPermissionCatalog {
		if _, ok := existing[want]; !ok {
			missingFromCatalog = append(missingFromCatalog, want)
		}
	}
	if len(missingFromCatalog) > 0 {
		fmt.Println("\n✗ FALTAN en tenant_permissions (backfill/migración de catálogo pendiente para este tenant):")
		for _, m := range missingFromCatalog {
			fmt.Printf("  - %s.%s\n", m[0], m[1])
		}
	} else {
		fmt.Println("\n✓ el catálogo de permisos de este tenant está completo frente al código vigente")
	}

	// Roles a inspeccionar.
	var roles []database.TenantRole
	rq := db
	if *roleName != "" {
		rq = rq.Where("name = ?", *roleName)
	} else {
		rq = rq.Where("is_system = ?", true)
	}
	if err := rq.Find(&roles).Error; err != nil {
		fmt.Printf("✗ leer tenant_roles: %v\n", err)
		return 1
	}
	if len(roles) == 0 {
		fmt.Println("\n(no se encontró ningún rol is_system=true; use --role=<nombre> para especificar uno)")
		return 0
	}

	for _, role := range roles {
		fmt.Printf("\n=== rol %q (id=%d, is_system=%v) ===\n", role.Name, role.ID, role.IsSystem)

		var granted []uint
		if err := db.Model(&database.TenantRolePermission{}).
			Where("role_id = ?", role.ID).
			Pluck("permission_id", &granted).Error; err != nil {
			fmt.Printf("  ✗ leer tenant_role_permissions: %v\n", err)
			continue
		}
		grantedSet := make(map[uint]bool, len(granted))
		for _, id := range granted {
			grantedSet[id] = true
		}

		var users []database.TenantUser
		db.Where("role_id = ?", role.ID).Find(&users)
		names := make([]string, 0, len(users))
		for _, u := range users {
			names = append(names, fmt.Sprintf("%s <%s> active=%v", u.Name, u.Email, u.Active))
		}
		sort.Strings(names)
		fmt.Printf("  usuarios con este rol: %d\n", len(users))
		for _, n := range names {
			fmt.Printf("    - %s\n", n)
		}

		var notGranted [][2]string
		for _, want := range tenantPermissionCatalog {
			pid, existsInCatalog := existing[want]
			if !existsInCatalog {
				continue // ya reportado arriba como faltante de catálogo
			}
			if !grantedSet[pid] {
				notGranted = append(notGranted, want)
			}
		}
		fmt.Printf("  permisos asignados: %d / %d disponibles en el catálogo de este tenant\n",
			len(grantedSet), len(existingPerms))
		if len(notGranted) > 0 {
			fmt.Println("  ✗ EXISTEN en el catálogo pero NO están asignados a este rol:")
			for _, m := range notGranted {
				fmt.Printf("    - %s.%s\n", m[0], m[1])
			}
		} else {
			fmt.Println("  ✓ tiene asignados todos los permisos que existen en el catálogo de este tenant")
		}
	}

	fmt.Println("\n(solo lectura: no se escribió nada)")
	return 0
}
