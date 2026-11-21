package branch

import (
	"fmt"
	"sync/atomic"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var permsSeq atomic.Int64

func permsDB(t *testing.T, catalog bool) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), permsSeq.Add(1))), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&database.TenantUserBranch{})
	for _, n := range []string{"A", "B", "C"} {
		db.Create(&database.TenantBranch{Name: n, Active: true})
	}
	if catalog {
		for _, a := range []string{"view_all", "switch", "manage"} {
			db.Create(&database.TenantPermission{Module: "branches", Action: a, Label: a})
		}
	}
	return db
}

func TestResolveViewAll(t *testing.T) {
	db := permsDB(t, true)
	if ResolveViewAll(db, nil, "Administrador", "pwd") {
		t.Error("el nombre del rol no debe dar acceso global con el catálogo sembrado")
	}
	if !ResolveViewAll(db, []string{PermViewAll}, "Otro", "pwd") {
		t.Error("branches.view_all debe dar acceso global")
	}
	if ResolveViewAll(db, []string{PermViewAll}, "Otro", "pin") {
		t.Error("el POS por PIN nunca tiene acceso global")
	}
	legacy := permsDB(t, false)
	if !ResolveViewAll(legacy, nil, "Administrador", "pwd") || ResolveViewAll(legacy, nil, "Vendedor", "pwd") {
		t.Error("tenant sin V176 conserva la regla por rol")
	}
}

func TestUserCanSwitchBranchAuth(t *testing.T) {
	db := permsDB(t, true)
	var switchPerm database.TenantPermission
	db.Where("action = ?", "switch").First(&switchPerm)
	mk := func(role string, assigned ...uint) database.TenantUser {
		r := database.TenantRole{Name: role + fmt.Sprint(len(assigned))}
		db.Create(&r)
		u := database.TenantUser{Name: role, Email: fmt.Sprintf("%s%d@x", role, len(assigned)), RoleID: r.ID, Active: true}
		db.Create(&u)
		for _, b := range assigned {
			db.Create(&database.TenantUserBranch{UserID: u.ID, BranchID: b})
		}
		return u
	}
	one := mk("uno", 1)
	if UserCanSwitchBranchAuth(db, "Vendedor", &one, "pwd") {
		t.Error("una sola sucursal: no cambia")
	}
	two := mk("dos", 1, 2)
	if !UserCanSwitchBranchAuth(db, "Vendedor", &two, "pwd") {
		t.Error("dos sucursales asignadas: puede cambiar entre ellas")
	}
	if UserCanSwitchBranchAuth(db, "Vendedor", &two, "pin") {
		t.Error("PIN con 2 sucursales sin permiso explícito: fijo en su sucursal")
	}
	db.Create(&database.TenantRolePermission{RoleID: one.RoleID, PermissionID: switchPerm.ID})
	if !UserCanSwitchBranchAuth(db, "Vendedor", &one, "pin") {
		t.Error("PIN con branches.switch explícito sí puede cambiar")
	}
	// Flag legacy can_switch_branch ya no concede nada con el catálogo sembrado.
	flag := mk("flag", 1)
	flag.CanSwitchBranch = true
	if UserCanSwitchBranchAuth(db, "Vendedor", &flag, "pwd") {
		t.Error("el flag can_switch_branch no debe bastar con el catálogo sembrado")
	}
}
