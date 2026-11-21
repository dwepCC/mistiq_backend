package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func giveCashbank(t *testing.T, db *gorm.DB, roleID uint, perms map[string]database.TenantPermission, actions ...string) {
	t.Helper()
	for _, a := range actions {
		if err := db.Create(&database.TenantRolePermission{RoleID: roleID, PermissionID: perms[a].ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// Roles con acceso a caja conservan Ingresos/Egresos/Reporte y (salvo el Vendedor de sistema) Cuentas y
// bancos; roles con cashbank.manage reciben las acciones de administración; roles sin caja no ganan
// nada; y una segunda corrida no cambia nada.
func TestV173CashbankFinePermissions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantRole{}, &database.TenantPermission{}, &database.TenantRolePermission{}); err != nil {
		t.Fatal(err)
	}
	perms := map[string]database.TenantPermission{}
	for _, a := range []string{"view", "open", "close", "movements", "manage"} {
		p := database.TenantPermission{Module: "cashbank", Action: a}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		perms[a] = p
	}
	vendedor := database.TenantRole{Name: "Vendedor", IsSystem: true}
	cajero := database.TenantRole{Name: "Cajero", IsSystem: true}
	admin := database.TenantRole{Name: "Administrador", IsSystem: true}
	sinCaja := database.TenantRole{Name: "Almacenero", IsSystem: true}
	custom := database.TenantRole{Name: "Vendedor", IsSystem: false} // mismo nombre pero rol propio del tenant
	for _, r := range []*database.TenantRole{&vendedor, &cajero, &admin, &sinCaja, &custom} {
		if err := db.Create(r).Error; err != nil {
			t.Fatal(err)
		}
	}
	giveCashbank(t, db, vendedor.ID, perms, "view", "open")
	giveCashbank(t, db, cajero.ID, perms, "open", "close", "movements")
	giveCashbank(t, db, admin.ID, perms, "manage")
	giveCashbank(t, db, custom.ID, perms, "view")

	for i := 0; i < 2; i++ {
		if err := (V188CashbankFinePermissions{}).Up(db); err != nil {
			t.Fatalf("Up #%d: %v", i+1, err)
		}
	}

	type want struct {
		role   database.TenantRole
		action string
		has    bool
	}
	cases := []want{
		{vendedor, "income_view", true}, {vendedor, "expense_view", true}, {vendedor, "report_view", true},
		{vendedor, "bank_view", false}, // el Vendedor de sistema NO recibe Cuentas y bancos
		{vendedor, "bank_manage", false}, {vendedor, "payment_methods_manage", false}, {vendedor, "delete_session", false},
		{cajero, "income_view", true}, {cajero, "bank_view", true}, {cajero, "bank_manage", false},
		{admin, "bank_view", true}, {admin, "bank_manage", true}, {admin, "payment_methods_manage", true}, {admin, "delete_session", true},
		{sinCaja, "income_view", false}, {sinCaja, "bank_view", false}, {sinCaja, "report_view", false},
		{custom, "bank_view", true}, // un rol propio llamado «Vendedor» conserva lo que ya veía
	}
	for _, c := range cases {
		if got := roleHas(t, db, c.role.ID, "cashbank", c.action); got != c.has {
			t.Errorf("%s (system=%v) cashbank.%s: got %v, want %v", c.role.Name, c.role.IsSystem, c.action, got, c.has)
		}
	}
	var n int64
	db.Model(&database.TenantPermission{}).Where("module = ?", "cashbank").Count(&n)
	if n != 5+int64(len(v188Perms)) {
		t.Errorf("catálogo cashbank: %d permisos", n)
	}
}
