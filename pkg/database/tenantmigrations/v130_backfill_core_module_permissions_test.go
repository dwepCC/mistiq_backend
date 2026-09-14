package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// El backfill debe conceder contacts/inventory/company/billing.send/dashboard.view a TODOS los
// roles existentes que aún no los tengan, sin duplicar los que ya los tuvieran, y sin fallar si el
// catálogo de permisos todavía no está sembrado en ese tenant.
func TestV130BackfillCoreModulePermissions(t *testing.T) {
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

	// Catálogo: incluye las 10 permisos que el backfill retro-concede, más uno ajeno
	// (sales.view) que no debe verse afectado.
	seedPerms := []database.TenantPermission{
		{Module: "contacts", Action: "view"},
		{Module: "contacts", Action: "create"},
		{Module: "contacts", Action: "edit"},
		{Module: "contacts", Action: "delete"},
		{Module: "inventory", Action: "view"},
		{Module: "inventory", Action: "manage"},
		{Module: "company", Action: "view"},
		{Module: "company", Action: "edit"},
		{Module: "billing", Action: "send"},
		{Module: "dashboard", Action: "view"},
		{Module: "sales", Action: "view"},
	}
	if err := db.Create(&seedPerms).Error; err != nil {
		t.Fatal(err)
	}
	var contactsView, salesView database.TenantPermission
	if err := db.Where("module = ? AND action = ?", "contacts", "view").First(&contactsView).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("module = ? AND action = ?", "sales", "view").First(&salesView).Error; err != nil {
		t.Fatal(err)
	}

	cajero := database.TenantRole{Name: "Cajero"}
	if err := db.Create(&cajero).Error; err != nil {
		t.Fatal(err)
	}
	// Vendedor ya tenía contacts.view asignado a mano: no debe duplicarse.
	vendedor := database.TenantRole{Name: "Vendedor"}
	if err := db.Create(&vendedor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantRolePermission{RoleID: vendedor.ID, PermissionID: contactsView.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantRolePermission{RoleID: vendedor.ID, PermissionID: salesView.ID}).Error; err != nil {
		t.Fatal(err)
	}

	if err := (V130BackfillCoreModulePermissions{}).Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Cajero: debe haber recibido las 10 permisos.
	var cajeroCount int64
	db.Model(&database.TenantRolePermission{}).Where("role_id = ?", cajero.ID).Count(&cajeroCount)
	if cajeroCount != int64(len(v130ModuleActions)) {
		t.Fatalf("cajero: got %d permisos, want %d", cajeroCount, len(v130ModuleActions))
	}

	// Vendedor: debe quedar con las 10 del backfill + sales.view (ajeno), sin duplicar
	// contacts.view.
	var vendedorPermIDs []uint
	db.Model(&database.TenantRolePermission{}).Where("role_id = ?", vendedor.ID).Pluck("permission_id", &vendedorPermIDs)
	if len(vendedorPermIDs) != len(v130ModuleActions)+1 {
		t.Fatalf("vendedor: got %d permisos, want %d", len(vendedorPermIDs), len(v130ModuleActions)+1)
	}
	var dup int64
	db.Model(&database.TenantRolePermission{}).
		Where("role_id = ? AND permission_id = ?", vendedor.ID, contactsView.ID).
		Count(&dup)
	if dup != 1 {
		t.Fatalf("vendedor: contacts.view duplicado, count = %d", dup)
	}

	// Re-ejecutar debe ser un no-op (idempotente).
	if err := (V130BackfillCoreModulePermissions{}).Up(db); err != nil {
		t.Fatalf("segunda ejecucion: %v", err)
	}
	var cajeroCountAfter int64
	db.Model(&database.TenantRolePermission{}).Where("role_id = ?", cajero.ID).Count(&cajeroCountAfter)
	if cajeroCountAfter != cajeroCount {
		t.Fatalf("cajero: la segunda ejecucion no fue idempotente, got %d, want %d", cajeroCountAfter, cajeroCount)
	}
}

// Si el catálogo de permisos aún no existe en el tenant (provisioning en curso), el backfill no
// debe fallar: simplemente no hay nada que retro-conceder todavía.
func TestV130BackfillCoreModulePermissions_EmptyCatalogIsNoop(t *testing.T) {
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
	role := database.TenantRole{Name: "Administrador"}
	if err := db.Create(&role).Error; err != nil {
		t.Fatal(err)
	}

	if err := (V130BackfillCoreModulePermissions{}).Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var count int64
	db.Model(&database.TenantRolePermission{}).Count(&count)
	if count != 0 {
		t.Fatalf("got %d filas, want 0", count)
	}
}
