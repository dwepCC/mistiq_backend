package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestV178SessionVersionBackfill(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantUser{}); err != nil {
		t.Fatal(err)
	}
	zero := database.TenantUser{Name: "a", Email: "a@x", Active: true}
	three := database.TenantUser{Name: "b", Email: "b@x", Active: true, BranchSessionVersion: 3}
	db.Create(&zero)
	db.Create(&three)

	for i := 0; i < 2; i++ {
		if err := (V193SessionVersionBackfill{}).Up(db); err != nil {
			t.Fatalf("Up #%d: %v", i+1, err)
		}
	}
	ver := func(id uint) uint {
		var u database.TenantUser
		db.First(&u, id)
		return u.BranchSessionVersion
	}
	if ver(zero.ID) != 1 {
		t.Errorf("usuario en 0 debe pasar a 1, quedó %d", ver(zero.ID))
	}
	if ver(three.ID) != 3 {
		t.Errorf("usuario en 3 no debe tocarse, quedó %d", ver(three.ID))
	}
}

func TestV178SessionVersionBackfill_NoColumnOrTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (V193SessionVersionBackfill{}).Up(db); err != nil {
		t.Fatalf("sin tabla no debe fallar: %v", err)
	}
	// Tabla legada sin la columna.
	if err := db.Exec("CREATE TABLE tenant_users (id integer primary key, name text, deleted_at datetime)").Error; err != nil {
		t.Fatal(err)
	}
	if err := (V193SessionVersionBackfill{}).Up(db); err != nil {
		t.Fatalf("sin columna no debe fallar: %v", err)
	}
}
