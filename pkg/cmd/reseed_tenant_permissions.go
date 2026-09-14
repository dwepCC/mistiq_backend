package cmd

import (
	"flag"
	"fmt"

	usersvc "tukifac/internal/users/service"
	"tukifac/pkg/database"
)

// RunReseedTenantPermissions completa el catálogo de permisos de UN tenant puntual (vía
// RoleService.SeedPermissions, ya idempotente tras el fix del bug de 2026-09-12) y re-asigna
// TODOS los permisos resultantes al rol "Administrador" — mismo tail que
// TenantService.RunMigrations, pero sin re-correr el motor de schema (estos tenants ya están al
// día en schema; lo único incompleto es el catálogo de permisos).
//
// Deliberadamente estrecho: exige --tenant explícito, nunca en lote — reparación quirúrgica para
// los tenants que audit-tenant-permission-catalog marque con catálogo incompleto.
func RunReseedTenantPermissions(args []string) int {
	fs := flag.NewFlagSet("reseed-tenant-permissions", flag.ExitOnError)
	slug := fs.String("tenant", "", "tenant por slug (obligatorio)")
	_ = fs.Parse(args)

	if *slug == "" {
		fmt.Println("uso: reseed-tenant-permissions --tenant=<slug>")
		return 1
	}

	var t database.Tenant
	if err := database.CentralDB.Where("slug = ?", *slug).First(&t).Error; err != nil {
		fmt.Printf("✗ tenant %q no encontrado: %v\n", *slug, err)
		return 1
	}

	db, err := database.OpenTenantDBForMigration(t.DBName)
	if err != nil {
		fmt.Printf("✗ abrir conexión: %v\n", err)
		return 1
	}
	defer database.CloseTenantDB(db)

	var before int64
	db.Model(&database.TenantPermission{}).Count(&before)

	roleSvc := usersvc.NewRoleService(db)
	if err := roleSvc.SeedPermissions(); err != nil {
		fmt.Printf("✗ SeedPermissions: %v\n", err)
		return 1
	}

	var after int64
	db.Model(&database.TenantPermission{}).Count(&after)
	fmt.Printf("catálogo: %d -> %d permisos\n", before, after)

	var adminRole database.TenantRole
	if err := db.Where("name = ?", "Administrador").First(&adminRole).Error; err != nil {
		fmt.Printf("✗ rol Administrador no encontrado: %v\n", err)
		return 1
	}

	perms, err := roleSvc.AllPermissions()
	if err != nil {
		fmt.Printf("✗ listar permisos: %v\n", err)
		return 1
	}
	permIDs := make([]uint, len(perms))
	for i, p := range perms {
		permIDs[i] = p.ID
	}
	if err := roleSvc.SetRolePermissions(adminRole.ID, permIDs); err != nil {
		fmt.Printf("✗ asignar permisos al Administrador: %v\n", err)
		return 1
	}

	fmt.Printf("✓ %s: Administrador (id=%d) ahora tiene %d/%d permisos del catálogo\n",
		*slug, adminRole.ID, len(permIDs), len(perms))
	return 0
}
