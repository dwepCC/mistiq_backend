package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// El backfill debe conceder modules.manage/ecommerce.{view,manage}/fleet.{view,manage} a TODOS los
// roles existentes que aún no los tengan, sin duplicar, y sin fallar si el catálogo todavía no
// existe en ese tenant.
func TestV131BackfillModulesEcommerceFleetPermissions(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantRole{},
		&database.TenantPermission{},
		&database.TenantRolePermission{},
	); err != nil {
		t.Fatal(err)
	}

	seedPerms := []database.TenantPermission{
		{Module: "modules", Action: "manage"},
		{Module: "ecommerce", Action: "view"},
		{Module: "ecommerce", Action: "manage"},
		{Module: "fleet", Action: "view"},
		{Module: "fleet", Action: "manage"},
		{Module: "sales", Action: "view"}, // ajeno, no debe verse afectado
	}
	if err := db.Create(&seedPerms).Error; err != nil {
		t.Fatal(err)
	}
	var fleetView database.TenantPermission
	if err := db.Where("module = ? AND action = ?", "fleet", "view").First(&fleetView).Error; err != nil {
		t.Fatal(err)
	}

	admin := database.TenantRole{Name: "Administrador"}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	// Vendedor ya tenía fleet.view asignado a mano: no debe duplicarse.
	vendedor := database.TenantRole{Name: "Vendedor"}
	if err := db.Create(&vendedor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantRolePermission{RoleID: vendedor.ID, PermissionID: fleetView.ID}).Error; err != nil {
		t.Fatal(err)
	}

	if err := (V131BackfillModulesEcommerceFleetPermissions{}).Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var adminCount int64
	db.Model(&database.TenantRolePermission{}).Where("role_id = ?", admin.ID).Count(&adminCount)
	if adminCount != int64(len(v131ModuleActions)) {
		t.Fatalf("administrador: got %d permisos, want %d", adminCount, len(v131ModuleActions))
	}

	var vendedorPermIDs []uint
	db.Model(&database.TenantRolePermission{}).Where("role_id = ?", vendedor.ID).Pluck("permission_id", &vendedorPermIDs)
	if len(vendedorPermIDs) != len(v131ModuleActions) {
		t.Fatalf("vendedor: got %d permisos, want %d (sin duplicar fleet.view)", len(vendedorPermIDs), len(v131ModuleActions))
	}

	// Idempotente.
	if err := (V131BackfillModulesEcommerceFleetPermissions{}).Up(db); err != nil {
		t.Fatalf("segunda ejecucion: %v", err)
	}
	var adminCountAfter int64
	db.Model(&database.TenantRolePermission{}).Where("role_id = ?", admin.ID).Count(&adminCountAfter)
	if adminCountAfter != adminCount {
		t.Fatalf("segunda ejecucion no fue idempotente: got %d, want %d", adminCountAfter, adminCount)
	}
}

// Caso real encontrado probando en local: un tenant que ya tenía su catálogo de permisos sembrado
// (RoleService.SeedPermissions solo inserta si la tabla está vacía, así que nunca vuelve a correr
// para tenants existentes) no tenía las filas modules/ecommerce/fleet en tenant_permissions — un
// primer intento de esta migración que solo buscaba el permiso y lo saltaba si no existía no
// concedía nada en ningún tenant existente. Debe CREAR la fila de catálogo si falta, no saltarla.
func TestV131BackfillModulesEcommerceFleetPermissions_CreatesMissingCatalogEntries(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantRole{},
		&database.TenantPermission{},
		&database.TenantRolePermission{},
	); err != nil {
		t.Fatal(err)
	}
	// Catálogo "ya sembrado" pero SIN modules/ecommerce/fleet (como cualquier tenant real
	// provisionado antes de que existieran estos permisos).
	if err := db.Create(&database.TenantPermission{Module: "sales", Action: "view", Label: "Ver ventas"}).Error; err != nil {
		t.Fatal(err)
	}
	role := database.TenantRole{Name: "Administrador"}
	if err := db.Create(&role).Error; err != nil {
		t.Fatal(err)
	}

	if err := (V131BackfillModulesEcommerceFleetPermissions{}).Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var catalogCount int64
	db.Model(&database.TenantPermission{}).
		Where("module IN ?", []string{"modules", "ecommerce", "fleet"}).
		Count(&catalogCount)
	if catalogCount != int64(len(v131ModuleActions)) {
		t.Fatalf("catálogo: got %d filas nuevas, want %d (no las creó)", catalogCount, len(v131ModuleActions))
	}

	var grantCount int64
	db.Model(&database.TenantRolePermission{}).Where("role_id = ?", role.ID).Count(&grantCount)
	if grantCount != int64(len(v131ModuleActions)) {
		t.Fatalf("administrador: got %d permisos concedidos, want %d", grantCount, len(v131ModuleActions))
	}
}
