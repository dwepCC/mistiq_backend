package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestV177ProductsManageGlobalPermission(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantRole{}, &database.TenantPermission{}, &database.TenantRolePermission{}); err != nil {
		t.Fatal(err)
	}
	perm := func(action string) database.TenantPermission {
		p := database.TenantPermission{Module: "products", Action: action, Label: action}
		db.Create(&p)
		return p
	}
	view, edit, create := perm("view"), perm("edit"), perm("create")
	almacen := database.TenantRole{Name: "Almacenero"}
	vend := database.TenantRole{Name: "Vendedor"}
	cust := database.TenantRole{Name: "Solo crear"}
	db.Create(&almacen)
	db.Create(&vend)
	db.Create(&cust)
	db.Create(&database.TenantRolePermission{RoleID: almacen.ID, PermissionID: edit.ID})
	db.Create(&database.TenantRolePermission{RoleID: vend.ID, PermissionID: view.ID})
	db.Create(&database.TenantRolePermission{RoleID: cust.ID, PermissionID: create.ID})

	for i := 0; i < 2; i++ {
		if err := (V192ProductsManageGlobalPermission{}).Up(db); err != nil {
			t.Fatalf("Up #%d: %v", i+1, err)
		}
	}
	var g database.TenantPermission
	if err := db.Where("module = 'products' AND action = 'manage_global'").First(&g).Error; err != nil {
		t.Fatal("permiso no creado")
	}
	has := func(roleID uint) int64 {
		var n int64
		db.Model(&database.TenantRolePermission{}).Where("role_id = ? AND permission_id = ?", roleID, g.ID).Count(&n)
		return n
	}
	if has(almacen.ID) != 1 || has(cust.ID) != 1 {
		t.Error("los roles que ya editaban/creaban productos deben conservar ese acceso (manage_global)")
	}
	if has(vend.ID) != 0 {
		t.Error("un rol que solo ve productos no debe recibir manage_global")
	}
}
