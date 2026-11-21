package service

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Pruebas de la versión de sesión revocable: cada evento que cambia lo que un access token ya emitido afirma
// (rol, permisos, contraseña, activo) incrementa tenant_users.branch_session_version y el middleware responde 409
// SESSION_UPDATED al token con la versión anterior, DESDE LA PRIMERA revocación.

var svSeq atomic.Int64

type svEnv struct {
	db     *gorm.DB
	roleA  database.TenantRole
	roleB  database.TenantRole
	owner  database.TenantUser
	target database.TenantUser
}

func newSVEnv(t *testing.T, startVersion uint) svEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), svSeq.Add(1))), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantUserBranch{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantBranch{Name: "Principal", Active: true, IsMain: true})
	e := svEnv{db: db, roleA: database.TenantRole{Name: "RolA"}, roleB: database.TenantRole{Name: "RolB"}}
	db.Create(&e.roleA)
	db.Create(&e.roleB)
	one := uint(1)
	e.owner = database.TenantUser{Name: "Dueño", Email: "owner@t.com", RoleID: e.roleA.ID, Active: true, HomeBranchID: &one}
	db.Create(&e.owner)
	e.target = database.TenantUser{Name: "Vend", Email: "v@t.com", RoleID: e.roleA.ID, Active: true, HomeBranchID: &one}
	db.Create(&e.target)
	db.Create(&database.TenantUserBranch{UserID: e.target.ID, BranchID: 1})
	db.Model(&database.TenantUser{}).Where("id = ?", e.target.ID).UpdateColumn("branch_session_version", startVersion)
	return e
}

func (e svEnv) version() uint {
	var u database.TenantUser
	e.db.First(&u, e.target.ID)
	return u.BranchSessionVersion
}

// call hace una petición autenticada como e.target con un token que lleva claimVersion.
func (e svEnv) call(claimVersion uint) (int, string) {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", e.db)
		c.Locals("tenant_claims", &middleware.TenantClaims{
			UserID: e.target.ID, RoleID: e.target.RoleID, RoleName: "x", AuthMethod: "pwd",
			ActiveBranchID: 1, BranchSessionVersion: claimVersion,
		})
		return c.Next()
	})
	app.Use(middleware.BranchContextMiddleware())
	app.Get("/api/x", func(c fiber.Ctx) error { return c.SendString("ok") })
	resp, err := app.Test(httptest.NewRequest("GET", "/api/x", nil))
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e svEnv) wantRevoked(t *testing.T, claimVersion uint, what string) {
	t.Helper()
	code, body := e.call(claimVersion)
	if code != 409 || !strings.Contains(body, branch.CodeSessionUpdated) {
		t.Fatalf("%s: token con versión %d debe recibir 409 SESSION_UPDATED, got %d %s", what, claimVersion, code, body)
	}
	if !strings.Contains(body, `"code"`) {
		t.Fatalf("el cuerpo del 409 debe incluir code: %s", body)
	}
}

// REPRODUCCIÓN del defecto: usuario sin backfill (versión 0) y token con claim 0. Antes, la primera revocación
// (0 -> 1) dejaba pasar el token porque claim 0 se trataba como "legado". Esta prueba falla con el código anterior.
func TestSessionVersion_FirstRevocationWorksForUnbackfilledUser(t *testing.T) {
	e := newSVEnv(t, 0)
	if code, _ := e.call(0); code != 200 {
		t.Fatalf("antes de revocar el token 0 debe pasar, got %d", code)
	}
	if err := branch.BumpSessionVersion(e.db, e.target.ID); err != nil {
		t.Fatal(err)
	}
	e.wantRevoked(t, 0, "primera revocación, usuario sin backfill")
}

// Tras V178 (usuarios en 1): token con claim 1, primera revocación -> 409; y un token legado con claim 0 se tolera
// mientras no haya revocación, pero cae en la primera.
func TestSessionVersion_AfterBackfill(t *testing.T) {
	e := newSVEnv(t, 1)
	if code, _ := e.call(1); code != 200 {
		t.Fatalf("token vigente debe pasar, got %d", code)
	}
	if code, _ := e.call(0); code != 200 {
		t.Fatalf("token legado (claim 0) con usuario en versión base debe tolerarse, got %d", code)
	}
	_ = branch.BumpSessionVersion(e.db, e.target.ID)
	e.wantRevoked(t, 1, "primera revocación tras V178")
	e.wantRevoked(t, 0, "token legado tras la primera revocación")
	if code, _ := e.call(e.version()); code != 200 {
		t.Fatalf("el token nuevo (versión %d) debe pasar, got %d", e.version(), code)
	}
}

func TestSessionVersion_RoleChangeBumps(t *testing.T) {
	e := newSVEnv(t, 1)
	err := NewUserService(e.db).Update(e.target.ID, UpdateUserInput{
		Name: "Vend", Email: "v@t.com", RoleID: e.roleB.ID, Active: true, BranchID: e.target.HomeBranchID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.version() < 2 {
		t.Fatalf("cambiar role_id debe incrementar la versión, quedó %d", e.version())
	}
	e.wantRevoked(t, 1, "cambio de rol")
}

func TestSessionVersion_UpdateWithoutRolePasswordOrActiveChangeDoesNotBump(t *testing.T) {
	e := newSVEnv(t, 1)
	err := NewUserService(e.db).Update(e.target.ID, UpdateUserInput{
		Name: "Otro nombre", Email: "v@t.com", RoleID: e.roleA.ID, Active: true, // sin BranchID: SetUserAssignedBranches ya sube la versión siempre
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.version() != 1 {
		t.Fatalf("editar solo el nombre no debe revocar: versión %d", e.version())
	}
}

func TestSessionVersion_RolePermissionsChangeBumpsAllUsersOfRole(t *testing.T) {
	e := newSVEnv(t, 1)
	other := database.TenantUser{Name: "Otro rol", Email: "o@t.com", RoleID: e.roleB.ID, Active: true}
	e.db.Create(&other)
	e.db.Model(&database.TenantUser{}).Where("id = ?", other.ID).UpdateColumn("branch_session_version", 1)
	perm := database.TenantPermission{Module: "sales", Action: "view", Label: "x"}
	e.db.Create(&perm)

	if err := NewRoleService(e.db).SetRolePermissions(e.roleA.ID, []uint{perm.ID}); err != nil {
		t.Fatal(err)
	}
	if e.version() < 2 {
		t.Fatalf("usuarios del rol deben subir de versión, quedó %d", e.version())
	}
	var o database.TenantUser
	e.db.First(&o, other.ID)
	if o.BranchSessionVersion != 1 {
		t.Fatalf("usuarios de OTRO rol no deben tocarse: %d", o.BranchSessionVersion)
	}
	e.wantRevoked(t, 1, "cambio de permisos del rol")
}

func TestSessionVersion_PasswordChangeBumps(t *testing.T) {
	e := newSVEnv(t, 1)
	// Cambio por el administrador (Update con password).
	if err := NewUserService(e.db).Update(e.target.ID, UpdateUserInput{
		Name: "Vend", Email: "v@t.com", RoleID: e.roleA.ID, Active: true, Password: "nueva-clave-123",
	}); err != nil {
		t.Fatal(err)
	}
	if e.version() != 2 {
		t.Fatalf("Update con contraseña: versión %d, quiere 2", e.version())
	}
	e.wantRevoked(t, 1, "contraseña cambiada por el admin")

	// Cambio propio (ChangeMyPasswordAPI -> ChangePassword).
	e2 := newSVEnv(t, 1)
	u := database.TenantUser{}
	e2.db.First(&u, e2.target.ID)
	_ = u.SetPassword("clave-actual-1")
	e2.db.Model(&database.TenantUser{}).Where("id = ?", u.ID).Update("password", u.Password)
	if err := NewUserService(e2.db).ChangePassword(u.ID, "clave-actual-1", "clave-nueva-123"); err != nil {
		t.Fatal(err)
	}
	if e2.version() != 2 {
		t.Fatalf("ChangePassword: versión %d, quiere 2", e2.version())
	}
	e2.wantRevoked(t, 1, "contraseña propia")
}

func TestSessionVersion_ActiveToggleAndUpdateBump(t *testing.T) {
	e := newSVEnv(t, 1)
	svc := NewUserService(e.db)
	if err := svc.ToggleActive(e.target.ID); err != nil { // desactivar
		t.Fatal(err)
	}
	if e.version() != 2 {
		t.Fatalf("ToggleActive: versión %d, quiere 2", e.version())
	}
	if err := svc.update(e.target.ID, UpdateUserInput{
		Name: "Vend", Email: "v@t.com", RoleID: e.roleA.ID, Active: true, BranchID: e.target.HomeBranchID,
	}); err != nil { // reactivar por edición
		t.Fatal(err)
	}
	if e.version() < 3 {
		t.Fatalf("reactivar por Update debe subir la versión: %d", e.version())
	}
	e.wantRevoked(t, 1, "activar/desactivar")
}
