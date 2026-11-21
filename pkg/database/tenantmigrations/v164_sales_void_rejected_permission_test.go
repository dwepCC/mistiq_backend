package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Solo Administrador recibe sales.void_rejected; Cajero no. Idempotente.
func TestV149SalesVoidRejectedPermission(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantRole{}, &database.TenantPermission{}, &database.TenantRolePermission{}); err != nil {
		t.Fatal(err)
	}
	admin := database.TenantRole{Name: "Administrador"}
	cajero := database.TenantRole{Name: "Cajero"}
	db.Create(&admin)
	db.Create(&cajero)

	for i := 0; i < 2; i++ {
		if err := (V164SalesVoidRejectedPermission{}).Up(db); err != nil {
			t.Fatalf("Up #%d: %v", i+1, err)
		}
	}

	var perm database.TenantPermission
	if err := db.Where("module = ? AND action = ?", "sales", "void_rejected").First(&perm).Error; err != nil {
		t.Fatalf("permiso no creado: %v", err)
	}
	var adminN, cajeroN int64
	db.Model(&database.TenantRolePermission{}).Where("role_id = ? AND permission_id = ?", admin.ID, perm.ID).Count(&adminN)
	db.Model(&database.TenantRolePermission{}).Where("role_id = ? AND permission_id = ?", cajero.ID, perm.ID).Count(&cajeroN)
	if adminN != 1 {
		t.Errorf("Administrador: %d grants, want 1", adminN)
	}
	if cajeroN != 0 {
		t.Errorf("Cajero: %d grants, want 0", cajeroN)
	}
}

func TestV149SalesVoidRejectedPermission_EmptyIsNoop(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (V164SalesVoidRejectedPermission{}).Up(db); err != nil {
		t.Fatalf("sin tablas debe ser no-op: %v", err)
	}
}
