package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupDeleteBranchTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantBranch{}, &database.TenantUser{}, &database.TenantUserBranch{}); err != nil {
		t.Fatal(err)
	}
	for _, b := range []database.TenantBranch{
		{ID: 1, Name: "FABRICA", IsMain: true, Active: true},
		{ID: 2, Name: "ALMACEN", Active: true},
		{ID: 3, Name: "TIENDA", Active: true},
	} {
		b := b
		if err := db.Create(&b).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func addUser(t *testing.T, db *gorm.DB, id uint, name string, home uint, assigned ...uint) {
	t.Helper()
	h := home
	u := database.TenantUser{ID: id, Name: name, Email: fmt.Sprintf("u%d@x.com", id), Password: "x", Active: true, HomeBranchID: &h, BranchID: &h}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	for _, b := range assigned {
		if err := db.Create(&database.TenantUserBranch{UserID: id, BranchID: b}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func branchExists(db *gorm.DB, id uint) bool {
	var n int64
	db.Model(&database.TenantBranch{}).Where("id = ?", id).Count(&n)
	return n > 0
}

func TestDeleteBranch_BlockedWhenItIsAUsersOnlyBranch(t *testing.T) {
	db := setupDeleteBranchTestDB(t)
	addUser(t, db, 13, "CORPODSYF", 3, 3)
	addUser(t, db, 14, "Otro", 1, 1, 3)

	err := NewCompanyService(db).DeleteBranch(3)
	var blocked *BranchDeleteBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("esperaba BranchDeleteBlockedError, got %v", err)
	}
	if !strings.Contains(err.Error(), "CORPODSYF") || strings.Contains(err.Error(), "Otro") {
		t.Fatalf("debe nombrar solo al usuario que se quedaría sin sucursal: %v", err)
	}
	if !branchExists(db, 3) {
		t.Fatal("la sucursal no debe eliminarse si deja un usuario sin sucursal")
	}
}

func TestDeleteBranch_ReassignsUsersWhoStillHaveOtherBranches(t *testing.T) {
	db := setupDeleteBranchTestDB(t)
	addUser(t, db, 16, "Multi", 3, 1, 3)

	if err := NewCompanyService(db).DeleteBranch(3); err != nil {
		t.Fatal(err)
	}
	if branchExists(db, 3) {
		t.Fatal("la sucursal debió eliminarse")
	}
	ids, err := branch.GetUserAssignedBranchIDs(db, 16)
	if err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("asignaciones tras eliminar: %v %v", ids, err)
	}
	var u database.TenantUser
	db.First(&u, 16)
	if u.HomeBranchID == nil || *u.HomeBranchID != 1 || u.BranchID == nil || *u.BranchID != 1 || u.CanSwitchBranch {
		t.Fatalf("la sucursal de origen debe pasar a la que le queda: home=%v branch=%v switch=%v", u.HomeBranchID, u.BranchID, u.CanSwitchBranch)
	}
}

func TestDeleteBranch_BlockedForMainBranch(t *testing.T) {
	db := setupDeleteBranchTestDB(t)
	var blocked *BranchDeleteBlockedError
	if err := NewCompanyService(db).DeleteBranch(1); !errors.As(err, &blocked) {
		t.Fatalf("la sucursal principal no se elimina: %v", err)
	}
	if !branchExists(db, 1) {
		t.Fatal("la principal debe seguir existiendo")
	}
}

func TestDeleteBranch_BlockedForLegacyUserWithoutAssignments(t *testing.T) {
	db := setupDeleteBranchTestDB(t)
	addUser(t, db, 20, "Legacy", 2)
	var blocked *BranchDeleteBlockedError
	if err := NewCompanyService(db).DeleteBranch(2); !errors.As(err, &blocked) {
		t.Fatalf("un usuario solo con sucursal de origen también se protege: %v", err)
	}
}

func TestDeleteBranch_MissingBranchIsNotAnError(t *testing.T) {
	db := setupDeleteBranchTestDB(t)
	if err := NewCompanyService(db).DeleteBranch(99); err != nil {
		t.Fatal(err)
	}
}

// Datos ya huérfanos (sucursal borrada con asignaciones que quedaron): no cuentan como sucursal del usuario.
func TestGetUserAssignedBranchIDs_IgnoresSoftDeletedBranches(t *testing.T) {
	db := setupDeleteBranchTestDB(t)
	addUser(t, db, 13, "CORPODSYF", 3, 3)
	if err := db.Delete(&database.TenantBranch{}, 3).Error; err != nil {
		t.Fatal(err)
	}
	ids, err := branch.GetUserAssignedBranchIDs(db, 13)
	if err != nil || len(ids) != 0 {
		t.Fatalf("una sucursal borrada no debe contar como asignada: %v %v", ids, err)
	}
}
