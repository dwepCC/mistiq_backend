package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Crea las tablas del módulo, siembra el plan de cuentas y entrega los permisos solo al Administrador
// de sistema; es idempotente.
func TestV174AccountingModule(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantRole{}, &database.TenantPermission{}, &database.TenantRolePermission{}); err != nil {
		t.Fatal(err)
	}
	admin := database.TenantRole{Name: "Administrador", IsSystem: true}
	vendedor := database.TenantRole{Name: "Vendedor", IsSystem: true}
	db.Create(&admin)
	db.Create(&vendedor)

	for i := 0; i < 2; i++ {
		if err := (V189AccountingModule{}).Up(db); err != nil {
			t.Fatalf("Up #%d: %v", i+1, err)
		}
	}
	for _, m := range []interface{}{&database.TenantAccountingSettings{}, &database.TenantAccountingAccount{}, &database.TenantSireSettings{}, &database.TenantSireTicket{}} {
		if !db.Migrator().HasTable(m) {
			t.Errorf("falta la tabla de %T", m)
		}
	}
	var n int64
	db.Model(&database.TenantAccountingAccount{}).Count(&n)
	if n != int64(len(v189Chart)) {
		t.Errorf("plan de cuentas: %d", n)
	}
	for _, p := range v189Perms {
		if !roleHas(t, db, admin.ID, "accounting", p.Action) {
			t.Errorf("el Administrador debe tener accounting.%s", p.Action)
		}
		if roleHas(t, db, vendedor.ID, "accounting", p.Action) {
			t.Errorf("el Vendedor no debe recibir accounting.%s", p.Action)
		}
	}
	db.Model(&database.TenantPermission{}).Where("module = ?", "accounting").Count(&n)
	if n != int64(len(v189Perms)) {
		t.Errorf("permisos: %d", n)
	}
}
