package database

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestTenantOwnerUserID_firstUser(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TenantUser{}); err != nil {
		t.Fatal(err)
	}
	var firstID uint
	for i := 1; i <= 3; i++ {
		u := &TenantUser{Name: "u", Email: fmt.Sprintf("u%d@t.com", i), RoleID: 1, Active: true}
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			firstID = u.ID
		}
	}
	ownerID, ok, err := TenantOwnerUserID(db)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || ownerID != firstID {
		t.Fatalf("expected owner id %d, got %d ok=%v", firstID, ownerID, ok)
	}
	isOwner, err := IsTenantOwnerUser(db, firstID)
	if err != nil || !isOwner {
		t.Fatalf("first user should be owner: isOwner=%v err=%v", isOwner, err)
	}
}
