package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Quien tenía billing.send recibe billing.view; quien no lo tenía, no. Idempotente.
func TestV171BillingViewPermission(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantRole{}, &database.TenantPermission{}, &database.TenantRolePermission{}); err != nil {
		t.Fatal(err)
	}
	send := database.TenantPermission{Module: "billing", Action: "send", Label: "send"}
	db.Create(&send)
	contador := database.TenantRole{Name: "Contador"}
	vendedor := database.TenantRole{Name: "Vendedor"}
	db.Create(&contador)
	db.Create(&vendedor)
	db.Create(&database.TenantRolePermission{RoleID: contador.ID, PermissionID: send.ID})

	for i := 0; i < 2; i++ {
		if err := (V186BillingViewPermission{}).Up(db); err != nil {
			t.Fatalf("Up #%d: %v", i+1, err)
		}
	}

	var view database.TenantPermission
	if err := db.Where("module = ? AND action = ?", "billing", "view").First(&view).Error; err != nil {
		t.Fatalf("permiso no creado: %v", err)
	}
	var contadorN, vendedorN int64
	db.Model(&database.TenantRolePermission{}).Where("role_id = ? AND permission_id = ?", contador.ID, view.ID).Count(&contadorN)
	db.Model(&database.TenantRolePermission{}).Where("role_id = ? AND permission_id = ?", vendedor.ID, view.ID).Count(&vendedorN)
	if contadorN != 1 {
		t.Errorf("Contador (tenía send): %d grants, want 1", contadorN)
	}
	if vendedorN != 0 {
		t.Errorf("Vendedor (sin send): %d grants, want 0", vendedorN)
	}
}
