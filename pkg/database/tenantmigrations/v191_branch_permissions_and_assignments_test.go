package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestV176BranchPermissionsAndAssignments(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantRole{}, &database.TenantPermission{}, &database.TenantRolePermission{},
		&database.TenantUser{}, &database.TenantBranch{}, &database.TenantUserBranch{}); err != nil {
		t.Fatal(err)
	}
	b1 := database.TenantBranch{Name: "Principal", Active: true, IsMain: true}
	b2 := database.TenantBranch{Name: "Norte", Active: true}
	db.Create(&b1)
	db.Create(&b2)
	admin := database.TenantRole{Name: "Administrador"}
	vend := database.TenantRole{Name: "Vendedor"}
	sup := database.TenantRole{Name: "Supervisor"}
	db.Create(&admin)
	db.Create(&vend)
	db.Create(&sup)

	home2 := b2.ID
	uAssigned := database.TenantUser{Name: "a", Email: "a@x", RoleID: vend.ID, Active: true}
	uHome := database.TenantUser{Name: "h", Email: "h@x", RoleID: vend.ID, Active: true, HomeBranchID: &home2}
	uNone := database.TenantUser{Name: "n", Email: "n@x", RoleID: vend.ID, Active: true}
	uSwitch := database.TenantUser{Name: "s", Email: "s@x", RoleID: sup.ID, Active: true, CanSwitchBranch: true}
	for _, u := range []*database.TenantUser{&uAssigned, &uHome, &uNone, &uSwitch} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	db.Create(&database.TenantUserBranch{UserID: uAssigned.ID, BranchID: b2.ID})

	for i := 0; i < 2; i++ { // idempotente
		if err := (V191BranchPermissionsAndAssignments{}).Up(db); err != nil {
			t.Fatalf("Up #%d: %v", i+1, err)
		}
	}

	has := func(roleID uint, action string) bool {
		var n int64
		db.Table("tenant_role_permissions").
			Joins("JOIN tenant_permissions p ON p.id = tenant_role_permissions.permission_id").
			Where("tenant_role_permissions.role_id = ? AND p.module = 'branches' AND p.action = ?", roleID, action).Count(&n)
		return n == 1
	}
	for _, a := range []string{"view_all", "switch", "manage"} {
		if !has(admin.ID, a) {
			t.Errorf("Administrador sin branches.%s", a)
		}
	}
	if has(vend.ID, "view_all") || has(vend.ID, "switch") || has(sup.ID, "view_all") || has(sup.ID, "manage") {
		t.Error("un rol no administrador no debe recibir view_all/manage ni switch sin usuarios que cambien de sucursal")
	}
	if !has(sup.ID, "switch") {
		t.Error("rol con usuarios can_switch_branch debe conservar branches.switch")
	}

	assignedTo := func(userID uint) []uint {
		var ids []uint
		db.Model(&database.TenantUserBranch{}).Where("user_id = ?", userID).Pluck("branch_id", &ids)
		return ids
	}
	if got := assignedTo(uAssigned.ID); len(got) != 1 || got[0] != b2.ID {
		t.Errorf("usuario ya asignado no debe cambiar: %v", got)
	}
	if got := assignedTo(uHome.ID); len(got) != 1 || got[0] != b2.ID {
		t.Errorf("usuario con home Norte debe asignarse a Norte: %v", got)
	}
	if got := assignedTo(uNone.ID); len(got) != 1 || got[0] != b1.ID {
		t.Errorf("usuario sin sucursal debe asignarse a la principal: %v", got)
	}
	var reload database.TenantUser
	db.First(&reload, uNone.ID)
	if reload.HomeBranchID == nil || *reload.HomeBranchID != b1.ID {
		t.Errorf("home_branch_id del usuario sin sucursal: %v", reload.HomeBranchID)
	}
}
