package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func roleHas(t *testing.T, db *gorm.DB, roleID uint, module, action string) bool {
	t.Helper()
	var n int64
	if err := db.Model(&database.TenantRolePermission{}).
		Joins("JOIN tenant_permissions ON tenant_permissions.id = tenant_role_permissions.permission_id").
		Where("tenant_role_permissions.role_id = ? AND tenant_permissions.module = ? AND tenant_permissions.action = ?", roleID, module, action).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// Cada rol con contacts.<acción> pasa a tener customers.<acción> y suppliers.<acción>; los
// contacts.* desaparecen del catálogo y de los roles; un rol sin contactos no gana nada; y una
// segunda corrida no cambia nada.
func TestV168SplitContactPermissions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantRole{}, &database.TenantPermission{}, &database.TenantRolePermission{}); err != nil {
		t.Fatal(err)
	}

	legacy := map[string]database.TenantPermission{}
	for _, a := range v183Actions {
		p := database.TenantPermission{Module: "contacts", Action: a}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		legacy[a] = p
	}
	vendedor := database.TenantRole{Name: "Vendedor"}
	sinContactos := database.TenantRole{Name: "Almacenero"}
	if err := db.Create(&vendedor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&sinContactos).Error; err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"view", "create"} {
		if err := db.Create(&database.TenantRolePermission{RoleID: vendedor.ID, PermissionID: legacy[a].ID}).Error; err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < 2; i++ { // la segunda corrida debe ser inocua
		if err := (V183SplitContactPermissions{}).Up(db); err != nil {
			t.Fatalf("Up #%d: %v", i+1, err)
		}
	}

	for _, module := range []string{"customers", "suppliers"} {
		for _, a := range []string{"view", "create"} {
			if !roleHas(t, db, vendedor.ID, module, a) {
				t.Errorf("vendedor debería tener %s.%s", module, a)
			}
		}
		for _, a := range []string{"edit", "delete"} {
			if roleHas(t, db, vendedor.ID, module, a) {
				t.Errorf("vendedor no debería tener %s.%s", module, a)
			}
		}
		if roleHas(t, db, sinContactos.ID, module, "view") {
			t.Errorf("el rol sin contactos no debería ganar %s.view", module)
		}
	}
	var left int64
	db.Model(&database.TenantPermission{}).Where("module = ?", "contacts").Count(&left)
	if left != 0 {
		t.Errorf("quedan %d permisos contacts.*", left)
	}
	var cat int64
	db.Model(&database.TenantPermission{}).Where("module IN ?", []string{"customers", "suppliers"}).Count(&cat)
	if cat != 8 {
		t.Errorf("catálogo customers/suppliers = %d, want 8", cat)
	}
}
