package middleware

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

var bcSeq atomic.Int64

type bcEnv struct {
	db *gorm.DB
}

func newBCEnv(t *testing.T, withCatalog bool) bcEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), bcSeq.Add(1))), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantUserBranch{}); err != nil {
		t.Fatal(err)
	}
	for i, n := range []string{"Principal", "Norte", "Sur"} {
		db.Create(&database.TenantBranch{Name: n, Active: true, IsMain: i == 0})
	}
	if withCatalog { // equivale a V176 aplicada
		db.Create(&database.TenantPermission{Module: "branches", Action: "view_all", Label: "x"})
	}
	return bcEnv{db: db}
}

func (e bcEnv) user(t *testing.T, name, role string, home uint, assigned ...uint) database.TenantUser {
	t.Helper()
	r := database.TenantRole{Name: role + name}
	e.db.Create(&r)
	u := database.TenantUser{Name: name, Email: name + "@x", RoleID: r.ID, Active: true}
	if home > 0 {
		h := home
		u.HomeBranchID = &h
	}
	if err := e.db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	for _, b := range assigned {
		e.db.Create(&database.TenantUserBranch{UserID: u.ID, BranchID: b})
	}
	return u
}

func (e bcEnv) call(u database.TenantUser, roleName string, perms []string, authMethod string, active uint, method, mode string) (int, string) {
	// "Ver todas" se decide con los permisos del rol en BD (no con los del JWT): se reflejan los permisos branches.* pedidos.
	for _, pm := range perms {
		if strings.HasPrefix(pm, "branches.") {
			var perm database.TenantPermission
			if e.db.Where("module = ? AND action = ?", "branches", strings.TrimPrefix(pm, "branches.")).First(&perm).Error == nil {
				e.db.FirstOrCreate(&database.TenantRolePermission{}, database.TenantRolePermission{RoleID: u.RoleID, PermissionID: perm.ID})
			}
		}
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", e.db)
		c.Locals("tenant_claims", &TenantClaims{UserID: u.ID, RoleID: u.RoleID, RoleName: roleName, Permissions: perms, AuthMethod: authMethod, ActiveBranchID: active})
		return c.Next()
	})
	app.Use(BranchContextMiddleware())
	h := func(c fiber.Ctx) error {
		ctx := branch.FromCtx(c)
		return c.SendString(fmt.Sprintf("active=%d all=%v switch=%v mode=%s", ctx.ActiveBranchID, ctx.CanViewAll, ctx.CanSwitch, ctx.Mode))
	}
	app.All("/api/x", h)
	req := httptest.NewRequest(method, "/api/x", nil)
	if mode != "" {
		req.Header.Set(branch.HeaderBranchMode, mode)
	}
	resp, err := app.Test(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestBranchContext_ViewAllIsPermissionNotRoleName(t *testing.T) {
	e := newBCEnv(t, true)
	// Rol llamado "Administrador" SIN el permiso: ya no ve todas las sucursales.
	fake := e.user(t, "fake", "x", 1, 1)
	if code, body := e.call(fake, "Administrador", nil, "pwd", 2, "GET", ""); code != 403 {
		t.Errorf("rol 'Administrador' sin permiso en sucursal ajena: %d %s, quiere 403", code, body)
	}
	// Rol con otro nombre pero con branches.view_all: ve todas.
	boss := e.user(t, "boss", "x", 1, 1)
	code, body := e.call(boss, "Gerente", []string{branch.PermViewAll}, "pwd", 3, "GET", "")
	if code != 200 || !strings.Contains(body, "all=true") {
		t.Errorf("rol con view_all: %d %s", code, body)
	}
}

func TestBranchContext_MultiBranchIsNotGlobal(t *testing.T) {
	e := newBCEnv(t, true)
	u := e.user(t, "multi", "x", 1, 1, 2)
	code, body := e.call(u, "Vendedor", nil, "pwd", 2, "GET", "")
	if code != 200 || !strings.Contains(body, "all=false") || !strings.Contains(body, "switch=true") {
		t.Errorf("multi asignada: %d %s", code, body)
	}
	if code, _ := e.call(u, "Vendedor", nil, "pwd", 3, "GET", ""); code != 403 {
		t.Errorf("multi en sucursal no asignada: %d, quiere 403", code)
	}
	// No puede pedir ALL_BRANCHES: sin view_all el header se ignora.
	if _, body := e.call(u, "Vendedor", nil, "pwd", 1, "GET", "all"); !strings.Contains(body, "mode=SINGLE_BRANCH") {
		t.Errorf("sin view_all el modo debe ser SINGLE_BRANCH: %s", body)
	}
}

func TestBranchContext_AllBranchesIsReadOnly(t *testing.T) {
	e := newBCEnv(t, true)
	u := e.user(t, "ver", "x", 1, 1)
	perms := []string{branch.PermViewAll}
	if code, body := e.call(u, "Gerente", perms, "pwd", 1, "GET", "all"); code != 200 || !strings.Contains(body, "mode=ALL_BRANCHES") {
		t.Errorf("GET en modo todas: %d %s", code, body)
	}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		code, body := e.call(u, "Gerente", perms, "pwd", 1, m, "all")
		if code != 403 || !strings.Contains(body, branch.CodeBranchReadOnly) {
			t.Errorf("%s en modo todas: %d %s, quiere 403 BRANCH_READ_ONLY", m, code, body)
		}
	}
	// Sin header, con sucursal concreta, escribe normal.
	if code, _ := e.call(u, "Gerente", perms, "pwd", 1, "POST", ""); code != 200 {
		t.Errorf("POST en sucursal concreta: %d", code)
	}
}

func TestBranchContext_PINNeverViewsAll(t *testing.T) {
	e := newBCEnv(t, true)
	u := e.user(t, "pin", "x", 1, 1)
	// Aunque el JWT traiga el permiso (o el rol sea Administrador), el POS por PIN no recibe acceso global.
	code, body := e.call(u, "Administrador", []string{branch.PermViewAll}, "pin", 2, "GET", "")
	if code != 403 {
		t.Errorf("PIN en otra sucursal: %d %s, quiere 403", code, body)
	}
}

func TestBranchContext_UserWithoutBranchIsBlocked(t *testing.T) {
	e := newBCEnv(t, true)
	u := e.user(t, "sin", "x", 0) // sin home ni asignaciones
	code, body := e.call(u, "Vendedor", nil, "pwd", 1, "GET", "")
	if code != 403 || !strings.Contains(body, branch.CodeBranchRequired) {
		t.Errorf("usuario sin sucursal: %d %s", code, body)
	}
	// En un tenant sin V176 se conserva el comportamiento anterior (cae en la principal).
	e2 := newBCEnv(t, false)
	u2 := e2.user(t, "sin", "x", 0)
	if code, body := e2.call(u2, "Vendedor", nil, "pwd", 1, "GET", ""); code != 200 {
		t.Errorf("tenant sin V176: %d %s", code, body)
	}
}

func TestBranchContext_LegacyAdminKeepsAccessBeforeV176(t *testing.T) {
	e := newBCEnv(t, false) // sin catálogo branches.*
	u := e.user(t, "adm", "x", 1, 1)
	code, body := e.call(u, "Administrador", nil, "pwd", 3, "GET", "")
	if code != 200 || !strings.Contains(body, "all=true") {
		t.Errorf("admin antes de V176: %d %s", code, body)
	}
}

func TestBranchContext_InactiveUserRejected(t *testing.T) {
	e := newBCEnv(t, true)
	u := e.user(t, "baja", "x", 1, 1)
	e.db.Model(&database.TenantUser{}).Where("id = ?", u.ID).Update("active", false)
	if code, _ := e.call(u, "Vendedor", nil, "pwd", 1, "GET", ""); code != 401 {
		t.Errorf("usuario inactivo: %d, quiere 401", code)
	}
}

// El permiso se lee de la BD, no del JWT: un token emitido ANTES de V176 (sin branches.view_all en sus claims) de un rol que
// ya lo tiene en BD debe ver todas las sucursales, igual que indica `can_view_all` en login/contexto. Antes el servidor ignoraba
// la cabecera X-Branch-Mode y devolvía solo la sucursal activa (vista "consolidada" falsa).
func TestBranchContext_ViewAllComesFromDBNotStaleJWT(t *testing.T) {
	e := newBCEnv(t, true)
	u := e.user(t, "viejo", "x", 1, 1)
	var perm database.TenantPermission
	e.db.Where("module = 'branches' AND action = 'view_all'").First(&perm)
	e.db.Create(&database.TenantRolePermission{RoleID: u.RoleID, PermissionID: perm.ID})
	// JWT sin permisos de sucursales (token viejo).
	code, body := e.call(u, "Gerente", []string{"sales.view"}, "pwd", 1, "GET", "all")
	if code != 200 || !strings.Contains(body, "mode=ALL_BRANCHES") || !strings.Contains(body, "all=true") {
		t.Errorf("rol con view_all en BD y JWT viejo: %d %s", code, body)
	}
}
