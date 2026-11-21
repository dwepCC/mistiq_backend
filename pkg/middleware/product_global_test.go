package middleware

import (
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

var pgSeq atomic.Int64

type pgEnv struct {
	db   *gorm.DB
	role database.TenantRole
}

func newPGEnv(t *testing.T, seeded bool, rolePerms ...string) pgEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), pgSeq.Add(1))), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantRole{}, &database.TenantPermission{}, &database.TenantRolePermission{}); err != nil {
		t.Fatal(err)
	}
	if seeded {
		db.Create(&database.TenantPermission{Module: "products", Action: "manage_global", Label: "x"})
	}
	role := database.TenantRole{Name: "Rol"}
	db.Create(&role)
	for _, action := range rolePerms {
		var p database.TenantPermission
		if err := db.Where("module = ? AND action = ?", "products", action).First(&p).Error; err != nil {
			p = database.TenantPermission{Module: "products", Action: action, Label: action}
			db.Create(&p)
		}
		db.Create(&database.TenantRolePermission{RoleID: role.ID, PermissionID: p.ID})
	}
	return pgEnv{db: db, role: role}
}

func (e pgEnv) call(t *testing.T, jwtPerms []string) int {
	t.Helper()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", e.db)
		c.Locals("tenant_claims", &TenantClaims{RoleID: e.role.ID, Permissions: jwtPerms})
		return c.Next()
	})
	app.Put("/p", RequirePermission("products.edit"), RequireGlobalCatalog(), func(c fiber.Ctx) error { return c.SendStatus(200) })
	resp, err := app.Test(httptest.NewRequest("PUT", "/p", nil))
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode
}

func TestRequireGlobalCatalog(t *testing.T) {
	// Rol que puede editar productos pero NO tiene manage_global en BD: 403.
	if c := newPGEnv(t, true, "edit").call(t, []string{"products.edit"}); c != 403 {
		t.Errorf("rol sin manage_global: %d, quiere 403", c)
	}
	// Rol con manage_global en BD: 200.
	if c := newPGEnv(t, true, "edit", "manage_global").call(t, []string{"products.edit", PermProductsManageGlobal}); c != 200 {
		t.Errorf("rol con manage_global: %d, quiere 200", c)
	}
	// products.manage también lo implica.
	if c := newPGEnv(t, true, "manage").call(t, []string{"products.manage"}); c != 200 {
		t.Errorf("products.manage implica el módulo: %d, quiere 200", c)
	}
	// Tenant sin V177: no restringe (despliegue escalonado).
	if c := newPGEnv(t, false, "edit").call(t, []string{"products.edit"}); c != 200 {
		t.Errorf("sin V177: %d, quiere 200", c)
	}
}

// Incidente del 11-nov-2026: V177 dio products.manage_global al rol, pero los JWT emitidos ANTES del deploy no lo traen.
// El permiso se decide con el rol en BD, no con el token: el administrador con sesión anterior debe poder crear/editar.
func TestRequireGlobalCatalog_StaleJWTBeforeV177StillWorks(t *testing.T) {
	e := newPGEnv(t, true, "edit", "manage_global")
	if c := e.call(t, []string{"products.edit"}); c != 200 { // JWT sin manage_global
		t.Errorf("JWT anterior a V177 con rol que ya tiene el permiso: %d, quiere 200", c)
	}
}

// Y al revés: si al rol se le quita el permiso, un JWT viejo que todavía lo trae no lo conserva (la BD manda).
func TestRequireGlobalCatalog_RevokedRoleLosesAccessDespiteJWT(t *testing.T) {
	e := newPGEnv(t, true, "edit")
	if c := e.call(t, []string{"products.edit", PermProductsManageGlobal}); c != 403 {
		t.Errorf("permiso retirado al rol pero presente en el JWT: %d, quiere 403", c)
	}
}
