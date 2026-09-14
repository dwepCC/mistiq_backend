package cmd

import (
	"flag"
	"fmt"

	"tukifac/pkg/database"
)

// RunAuditTenantPermissionCatalog escanea TODOS los tenants comparando cuántas filas tiene su
// tenant_permissions contra el tamaño del catálogo vigente en el código (len(tenantPermissionCatalog),
// definido en diagnose_tenant_permissions.go) — solo lectura.
//
// Por qué existe: RoleService.SeedPermissions() solo siembra el catálogo COMPLETO si
// tenant_permissions está totalmente vacía. Migraciones de schema como V131/V132 insertan (vía
// FirstOrCreate) sus propias filas nuevas de catálogo para TODO tenant, incluyendo uno recién
// creado — y como esas migraciones corren ANTES de que ProvisionTenantSeed cree los roles y
// ANTES de la llamada explícita a SeedPermissions() en TenantService.CreateTenant, un tenant
// nuevo puede terminar con tenant_permissions ya no-vacía (solo las filas que esas migraciones
// adelantaron) en el momento en que SeedPermissions() se ejecuta — su guarda "if count > 0
// return nil" se dispara de más, y el resto del catálogo (los permisos "base": dashboard, users,
// roles, company, contacts, products, sales, purchases, cashbank básico, billing.send,
// memberships) nunca se siembra para ese tenant. El Administrador de ese tenant termina con SOLO
// los permisos que esas migraciones adelantaron, no con el catálogo completo.
func RunAuditTenantPermissionCatalog(args []string) int {
	fs := flag.NewFlagSet("audit-tenant-permission-catalog", flag.ExitOnError)
	activeOnly := fs.Bool("active-only", true, "omitir tenants no activos")
	_ = fs.Parse(args)

	tenants, err := database.ListTenantsForMigration(*activeOnly)
	if err != nil {
		fmt.Printf("✗ no se pudo listar tenants: %v\n", err)
		return 1
	}

	want := len(tenantPermissionCatalog)
	fmt.Printf("audit-tenant-permission-catalog tenants=%d catálogo_código=%d\n", len(tenants), want)

	affected := 0
	for _, t := range tenants {
		db, err := database.OpenTenantDBForMigration(t.DBName)
		if err != nil {
			fmt.Printf("  ✗ %-24s abrir conexión: %v\n", t.Slug, err)
			continue
		}
		var count int64
		countErr := db.Model(&database.TenantPermission{}).Count(&count).Error
		database.CloseTenantDB(db)
		if countErr != nil {
			fmt.Printf("  ✗ %-24s contar tenant_permissions: %v\n", t.Slug, countErr)
			continue
		}
		if int(count) != want {
			affected++
			fmt.Printf("  ✗ %-24s catálogo=%d/%d (created_at=%s)\n",
				t.Slug, count, want, t.CreatedAt.Format("2006-01-02 15:04:05"))
		}
	}

	fmt.Printf("\ntenants_con_catálogo_incompleto=%d de %d\n", affected, len(tenants))
	fmt.Println("(solo lectura: no se escribió nada)")
	return 0
}
