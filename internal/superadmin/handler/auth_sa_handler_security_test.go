package handler

// Fase 4 — tests de invalidación de sesión (TokenVersion) a través de los flujos existentes de
// AuthSAHandler (reset/cambio de contraseña, cambio de rol) y de saPermissionsForUser (permisos
// resueltos al login). Los tests de autenticación pura (firma/expiración/Active/DeletedAt/
// TokenVersion contra el middleware) viven en pkg/middleware/jwt_superadmin_test.go — aquí solo
// se cubre lo que es responsabilidad de este paquete: qué operaciones deben incrementar
// TokenVersion, y que un JWT emitido antes de esa operación deja de servir.
//
// Grupo 7, Paso E: newAuthSATestApp ahora inyecta sa_claims completo (no solo sa_user_role/
// sa_user_id) — los handlers de escritura de usuarios usan CanDelegateAll/HasSAPermission, que
// necesitan el claims completo. El toggle admin/superadmin (antes parte de UpdateUserAPI) ahora
// vive en PUT /users/:id/system-role (ChangeUserSystemRoleAPI) — ver
// TestAuthSAHandler_ChangeUserSystemRoleAPI_IncrementsTokenVersion.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"tukifac/config"
	"tukifac/internal/superadmin/service"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

const testAuthSAJWTSecret = "test-auth-sa-secret"

func setupAuthSATestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.SuperAdminUser{}, &database.SARole{}, &database.SAPermission{}, &database.SARolePermission{},
		&database.AuditLog{},
	); err != nil {
		t.Fatal(err)
	}
	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })
	return db
}

func setAuthSATestConfig(t *testing.T) {
	t.Helper()
	prev := config.AppConfig
	config.AppConfig = &config.Config{AppEnv: "development", SAJWTSecret: testAuthSAJWTSecret}
	t.Cleanup(func() { config.AppConfig = prev })
}

func createAuthSAUser(t *testing.T, db *gorm.DB, email, role string, tokenVersion uint) database.SuperAdminUser {
	t.Helper()
	u := database.SuperAdminUser{Name: "Test", Email: email, Role: role, Active: true, TokenVersion: tokenVersion}
	if err := u.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

// newAuthSATestApp monta AuthSAHandler inyectando los locals que dejaría SuperAdminAuthAPI —
// misma técnica que pkg/middleware/master_access_test.go para probar el handler aislado del
// parseo del JWT. claims debe traer al menos UserID/Role (y Permissions si el test necesita
// probar el techo de delegación) — este helper no asume ningún valor por defecto.
func newAuthSATestApp(claims *middleware.SuperAdminClaims) *fiber.App {
	h := NewAuthSAHandler()
	app := fiber.New()
	inject := func(c fiber.Ctx) error {
		c.Locals("sa_claims", claims)
		c.Locals("sa_user_id", claims.UserID)
		c.Locals("sa_user_role", claims.Role)
		return c.Next()
	}
	app.Post("/api/superadmin/users", inject, h.CreateUserAPI)
	app.Put("/api/superadmin/users/:id", inject, h.UpdateUserAPI)
	app.Put("/api/superadmin/users/:id/role", inject, h.ChangeUserRoleAPI)
	app.Put("/api/superadmin/users/:id/system-role", inject, h.ChangeUserSystemRoleAPI)
	app.Patch("/api/superadmin/users/:id/status", inject, h.ChangeUserStatusAPI)
	app.Post("/api/superadmin/users/:id/password", inject, h.ResetUserPasswordAPI)
	app.Delete("/api/superadmin/users/:id", inject, h.DestroyUserAPI)
	app.Post("/api/superadmin/me/password", inject, h.ChangeMyPasswordAPI)
	return app
}

func superadminClaims(userID uint) *middleware.SuperAdminClaims {
	return &middleware.SuperAdminClaims{UserID: userID, Role: "superadmin"}
}

func adminClaims(userID uint, permissions ...string) *middleware.SuperAdminClaims {
	return &middleware.SuperAdminClaims{UserID: userID, Role: "admin", Permissions: permissions}
}

func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

// mintTestSAToken firma un JWT real con middleware.SuperAdminClaims, para probarlo contra el
// middleware real (no simulado) en TestAuthSAHandler_PasswordChange_InvalidatesOldJWT.
func mintTestSAToken(t *testing.T, userID uint, role string, tokenVersion uint) string {
	t.Helper()
	claims := &middleware.SuperAdminClaims{
		UserID: userID, Email: "u@example.com", Role: role, Type: "superadmin",
		TokenVersion: tokenVersion, Permissions: []string{"*"}, SAJWTVersion: middleware.CurrentSuperAdminJWTVersion(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte(testAuthSAJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func statusThroughRealAuthMiddleware(t *testing.T, token string) int {
	t.Helper()
	app := fiber.New()
	app.Get("/api/superadmin/protegida", middleware.SuperAdminAuthAPI(), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	req := httptest.NewRequest("GET", "/api/superadmin/protegida", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// 11. Reset de contraseña incrementa TokenVersion.
func TestAuthSAHandler_ResetUserPasswordAPI_IncrementsTokenVersion(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "root@example.com", "superadmin", 0)
	target := createAuthSAUser(t, db, "target@example.com", "admin", 3)

	app := newAuthSATestApp(superadminClaims(actor.ID))
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/superadmin/users/%d/password", target.ID),
		jsonBody(t, map[string]string{"new_password": "newpassword123"}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var reloaded database.SuperAdminUser
	db.First(&reloaded, target.ID)
	if reloaded.TokenVersion != 4 {
		t.Fatalf("TokenVersion = %d, want 4 (3+1)", reloaded.TokenVersion)
	}
}

// 12. Cambio de contraseña (propia) incrementa TokenVersion.
func TestAuthSAHandler_ChangeMyPasswordAPI_IncrementsTokenVersion(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	self := createAuthSAUser(t, db, "self@example.com", "admin", 5)

	app := newAuthSATestApp(adminClaims(self.ID))
	req := httptest.NewRequest("POST", "/api/superadmin/me/password",
		jsonBody(t, map[string]string{"current_password": "password123", "new_password": "newpassword123"}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var reloaded database.SuperAdminUser
	db.First(&reloaded, self.ID)
	if reloaded.TokenVersion != 6 {
		t.Fatalf("TokenVersion = %d, want 6 (5+1)", reloaded.TokenVersion)
	}
}

// 13. El JWT anterior queda inválido tras el reset/cambio de contraseña — probado contra el
// middleware REAL (middleware.SuperAdminAuthAPI), no simulado.
func TestAuthSAHandler_PasswordChange_InvalidatesOldJWT(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "root@example.com", "superadmin", 0)
	target := createAuthSAUser(t, db, "target@example.com", "admin", 0)

	oldToken := mintTestSAToken(t, target.ID, "admin", 0)
	if got := statusThroughRealAuthMiddleware(t, oldToken); got != fiber.StatusOK {
		t.Fatalf("token antes del reset: status = %d, want 200", got)
	}

	app := newAuthSATestApp(superadminClaims(actor.ID))
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/superadmin/users/%d/password", target.ID),
		jsonBody(t, map[string]string{"new_password": "newpassword123"}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("reset status = %d, want 200", resp.StatusCode)
	}

	if got := statusThroughRealAuthMiddleware(t, oldToken); got != fiber.StatusUnauthorized {
		t.Fatalf("token tras el reset: status = %d, want 401", got)
	}
}

// 14. Cambio de rol de SISTEMA (admin↔superadmin, PUT /users/:id/system-role) incrementa
// TokenVersion — antes de este grupo este toggle vivía en UpdateUserAPI; ahora es
// ChangeUserSystemRoleAPI, con las mismas garantías de invalidación de sesión.
func TestAuthSAHandler_ChangeUserSystemRoleAPI_IncrementsTokenVersion(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "root@example.com", "superadmin", 0)
	target := createAuthSAUser(t, db, "target@example.com", "admin", 7)

	app := newAuthSATestApp(superadminClaims(actor.ID))
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d/system-role", target.ID),
		jsonBody(t, map[string]string{"role": "superadmin"}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var reloaded database.SuperAdminUser
	db.First(&reloaded, target.ID)
	if reloaded.TokenVersion != 8 {
		t.Fatalf("TokenVersion = %d, want 8 (7+1)", reloaded.TokenVersion)
	}
	if reloaded.Role != "superadmin" {
		t.Fatalf("Role = %q, want superadmin", reloaded.Role)
	}
}

// Cambios que NO son de rol (solo nombre) no deben incrementar TokenVersion — no son un evento de
// seguridad, y hacerlo forzaría relogins innecesarios. UpdateUserAPI ya no acepta "role" en
// absoluto (ver mass-assignment test dedicado), así que esto ahora prueba directamente el caso
// normal de uso.
func TestAuthSAHandler_UpdateUserAPI_NameChange_DoesNotIncrementTokenVersion(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "root@example.com", "superadmin", 0)
	target := createAuthSAUser(t, db, "target@example.com", "admin", 7)

	app := newAuthSATestApp(superadminClaims(actor.ID))
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d", target.ID),
		jsonBody(t, map[string]string{"name": "Nuevo Nombre"}))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var reloaded database.SuperAdminUser
	db.First(&reloaded, target.ID)
	if reloaded.TokenVersion != 7 {
		t.Fatalf("TokenVersion = %d, want 7 (sin cambios — no fue cambio de rol)", reloaded.TokenVersion)
	}
}

// 15. Cambio de permisos efectivos de un rol invalida a los usuarios que lo tienen asignado
// (mecanismo real, ver SARoleService.SetRolePermissions — cubierto en detalle en
// internal/superadmin/service/sa_role_service_test.go; aquí se confirma end-to-end desde este
// paquete).
func TestAuthSAHandler_RolePermissionsChange_InvalidatesAssignedUsers(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	svc := service.NewSARoleService(db)

	role, err := svc.Create("Soporte Jr", "")
	if err != nil {
		t.Fatalf("Create role: %v", err)
	}
	perm := database.SAPermission{Module: "empresas", Action: "view", Label: "Ver empresas"}
	if err := db.Create(&perm).Error; err != nil {
		t.Fatal(err)
	}

	user := createAuthSAUser(t, db, "u@example.com", "admin", 2)
	if err := db.Model(&user).Update("role_id", role.ID).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.SetRolePermissions(role.ID, []uint{perm.ID}); err != nil {
		t.Fatalf("SetRolePermissions: %v", err)
	}

	var reloaded database.SuperAdminUser
	db.First(&reloaded, user.ID)
	if reloaded.TokenVersion != 3 {
		t.Fatalf("TokenVersion = %d, want 3 (2+1)", reloaded.TokenVersion)
	}
}

// 16. Usuario sin RoleID no obtiene permisos (y superadmin obtiene "*", usuario con rol obtiene
// las claves de su rol) — saPermissionsForUser es lo que LoginAPI usa para construir el JWT.
func TestSaPermissionsForUser(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	svc := service.NewSARoleService(db)

	role, err := svc.Create("Soporte", "")
	if err != nil {
		t.Fatalf("Create role: %v", err)
	}
	perm := database.SAPermission{Module: "empresas", Action: "view", Label: "Ver empresas"}
	if err := db.Create(&perm).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.SetRolePermissions(role.ID, []uint{perm.ID}); err != nil {
		t.Fatalf("SetRolePermissions: %v", err)
	}

	superadmin := database.SuperAdminUser{Role: "superadmin"}
	if got := saPermissionsForUser(&superadmin); len(got) != 1 || got[0] != "*" {
		t.Fatalf("permisos de superadmin = %v, want [\"*\"]", got)
	}

	noRole := database.SuperAdminUser{Role: "admin", RoleID: nil}
	if got := saPermissionsForUser(&noRole); len(got) != 0 {
		t.Fatalf("usuario sin RoleID obtuvo permisos: %v, want []", got)
	}

	withRole := database.SuperAdminUser{Role: "admin", RoleID: &role.ID}
	got := saPermissionsForUser(&withRole)
	if len(got) != 1 || got[0] != "empresas.view" {
		t.Fatalf("permisos del rol = %v, want [empresas.view]", got)
	}
}
