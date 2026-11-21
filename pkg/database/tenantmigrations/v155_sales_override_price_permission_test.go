package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// El blanket grant debe conceder sales.override_price a TODOS los roles existentes que aún no lo
// tengan (decisión explícita del usuario: nadie pierde la capacidad de editar precio el día del
// deploy), sin duplicar en un rol que ya lo tuviera, y sin fallar si el catálogo de permisos
// todavía no está sembrado en ese tenant (provisioning en curso).
func TestV140SalesOverridePricePermission(t *testing.T) {
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

	admin := database.TenantRole{Name: "Administrador"}
	cajero := database.TenantRole{Name: "Cajero"}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&cajero).Error; err != nil {
		t.Fatal(err)
	}

	// Cajero ya lo tiene asignado a mano (ej. un tenant que ya venía probando el permiso antes del
	// release) — no debe duplicarse.
	var preExisting database.TenantPermission
	if err := db.Where(database.TenantPermission{Module: "sales", Action: "override_price"}).
		Attrs(database.TenantPermission{Label: "preexistente"}).
		FirstOrCreate(&preExisting).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantRolePermission{RoleID: cajero.ID, PermissionID: preExisting.ID}).Error; err != nil {
		t.Fatal(err)
	}

	if err := (V155SalesOverridePricePermission{}).Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var perm database.TenantPermission
	if err := db.Where("module = ? AND action = ?", "sales", "override_price").First(&perm).Error; err != nil {
		t.Fatalf("el permiso no quedó en el catálogo: %v", err)
	}
	if perm.ID != preExisting.ID {
		t.Fatalf("se creó una segunda fila de catálogo en vez de reusar la existente")
	}

	var adminCount, cajeroCount int64
	db.Model(&database.TenantRolePermission{}).Where("role_id = ? AND permission_id = ?", admin.ID, perm.ID).Count(&adminCount)
	db.Model(&database.TenantRolePermission{}).Where("role_id = ? AND permission_id = ?", cajero.ID, perm.ID).Count(&cajeroCount)
	if adminCount != 1 {
		t.Errorf("Administrador: got %d grants, want 1", adminCount)
	}
	if cajeroCount != 1 {
		t.Errorf("Cajero: got %d grants, want 1 (no duplicado)", cajeroCount)
	}

	// Re-ejecutar debe ser un no-op idempotente.
	if err := (V155SalesOverridePricePermission{}).Up(db); err != nil {
		t.Fatalf("segunda ejecución: %v", err)
	}
	var totalAfter int64
	db.Model(&database.TenantRolePermission{}).Where("permission_id = ?", perm.ID).Count(&totalAfter)
	if totalAfter != 2 {
		t.Fatalf("segunda ejecución no fue idempotente: got %d filas, want 2 (Administrador + Cajero)", totalAfter)
	}
}

// Si el catálogo/roles todavía no existen (provisioning en curso), la migración no debe fallar.
func TestV140SalesOverridePricePermission_EmptyIsNoop(t *testing.T) {
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

	if err := (V155SalesOverridePricePermission{}).Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var count int64
	db.Model(&database.TenantRolePermission{}).Count(&count)
	if count != 0 {
		t.Fatalf("got %d filas, want 0", count)
	}
}
