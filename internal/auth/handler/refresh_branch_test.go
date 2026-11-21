package handler

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Al renovar el token se conserva la sucursal activa solo si el usuario puede acceder a ella.
func TestAllowedBranchForRefresh(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	for i, active := range []bool{true, true, true, false} {
		b := database.TenantBranch{Name: fmt.Sprintf("S%d", i+1), Active: true, IsMain: i == 0}
		if err := db.Create(&b).Error; err != nil {
			t.Fatal(err)
		}
		if !active {
			db.Model(&b).Update("active", false)
		}
	}
	if err := db.AutoMigrate(&database.TenantUserBranch{}); err != nil {
		t.Fatal(err)
	}
	u := database.TenantUser{Name: "u", Email: "u@x", Active: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	for _, bid := range []uint{1, 2} {
		if err := db.Create(&database.TenantUserBranch{UserID: u.ID, BranchID: bid}).Error; err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		role string
		want uint
		ok   bool
	}{
		{"Vendedor", 2, true},       // asignada
		{"Vendedor", 3, false},      // existe pero no asignada
		{"Vendedor", 4, false},      // inactiva
		{"Vendedor", 99, false},     // inexistente
		{"Administrador", 3, true},  // ve todas
		{"Administrador", 4, false}, // inactiva incluso para el admin
	}
	for _, tc := range cases {
		if got := allowedBranchForRefresh(db, &u, tc.role, tc.want); got != tc.ok {
			t.Errorf("%s sucursal %d: got %v want %v", tc.role, tc.want, got, tc.ok)
		}
	}
}
