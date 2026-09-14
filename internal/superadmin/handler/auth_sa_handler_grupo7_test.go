package handler

// Fase 5, etapa 3, Grupo 7, Paso E: tests de AuthSAHandler que son responsabilidad de la capa
// HTTP — no del servicio (ya cubierto exhaustivamente en
// internal/superadmin/service/sa_user_service_test.go): mass-assignment (DTOs explícitos),
// auto-servicio vs. permiso granular en UpdateUserAPI, auditoría, e invalidación de JWT vía el
// middleware real para los flujos que sa_user_service_test.go no puede probar (ese archivo no
// pasa por SuperAdminAuthAPI).

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
)

// ==================== Mass assignment (§7, §32) ====================

// El test obligatorio del spec: un body que intenta tocar TODOS los campos protegidos a la vez
// contra el endpoint que solo debería poder cambiar name/email.
func TestUpdateUserAPI_MassAssignment_IgnoresProtectedFields(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "root@example.com", "superadmin", 0)
	target := createAuthSAUser(t, db, "target@example.com", "admin", 0)
	originalHash := target.Password

	app := newAuthSATestApp(superadminClaims(actor.ID))
	body := map[string]any{
		"name":          "Nombre Nuevo",
		"email":         "nuevo@example.com",
		"role":          "superadmin",
		"role_id":       999,
		"active":        false,
		"token_version": 999,
		"deleted_at":    "2020-01-01T00:00:00Z",
	}
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d", target.ID), jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// El status puede ser 200 (name/email sí se aplican) — lo único que importa es que los campos
	// protegidos queden intactos.
	_ = resp.StatusCode

	var reloaded database.SuperAdminUser
	if err := db.First(&reloaded, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.Role != "admin" {
		t.Errorf("Role = %q, want admin sin cambios (mass assignment coló \"role\")", reloaded.Role)
	}
	if reloaded.RoleID != nil {
		t.Errorf("RoleID = %v, want nil sin cambios (mass assignment coló \"role_id\")", reloaded.RoleID)
	}
	if !reloaded.Active {
		t.Error("Active = false, want true sin cambios (mass assignment coló \"active\")")
	}
	if reloaded.TokenVersion != 0 {
		t.Errorf("TokenVersion = %d, want 0 sin cambios (mass assignment coló \"token_version\")", reloaded.TokenVersion)
	}
	if reloaded.Password != originalHash {
		t.Error("Password cambió — mass assignment no debería poder tocar la contraseña")
	}
	if reloaded.DeletedAt.Valid {
		t.Error("DeletedAt quedó seteado — mass assignment no debería poder eliminar el usuario")
	}
}

// ==================== UpdateUserAPI — autoservicio vs. permiso granular ====================

func TestUpdateUserAPI_SelfCanEditOwnNameEmailWithoutAnyPermission(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	self := createAuthSAUser(t, db, "self@example.com", "admin", 0)

	app := newAuthSATestApp(adminClaims(self.ID)) // sin ningún permiso
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d", self.ID),
		jsonBody(t, map[string]string{"name": "Yo Mismo"}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 (autoservicio de nombre propio)", resp.StatusCode)
	}

	var reloaded database.SuperAdminUser
	db.First(&reloaded, self.ID)
	if reloaded.Name != "Yo Mismo" {
		t.Fatal("el nombre propio no se actualizó")
	}
}

func TestUpdateUserAPI_EditingAnotherUserRequiresPermission(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	other := createAuthSAUser(t, db, "other@example.com", "admin", 0)

	app := newAuthSATestApp(adminClaims(actor.ID)) // sin usuarios_central.update
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d", other.ID),
		jsonBody(t, map[string]string{"name": "Cambiado"}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (editar a otro sin usuarios_central.update)", resp.StatusCode)
	}
}

func TestUpdateUserAPI_EditingAnotherUserWithPermissionSucceeds(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	other := createAuthSAUser(t, db, "other@example.com", "admin", 0)

	app := newAuthSATestApp(adminClaims(actor.ID, "usuarios_central.update"))
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d", other.ID),
		jsonBody(t, map[string]string{"name": "Cambiado"}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// ==================== Creación con role_id según capacidad (§33) ====================

func TestCreateUserAPI_RoleIDWithinActorCeiling_Succeeds(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	viewPerm := database.SAPermission{Module: "empresas", Action: "view", Label: "Ver empresas"}
	db.Create(&viewPerm)
	role := database.SARole{Name: "Soporte"}
	db.Create(&role)
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: viewPerm.ID})

	app := newAuthSATestApp(adminClaims(actor.ID, "usuarios_central.create", "empresas.view"))
	resp, out := doRequest(t, app, jsonRequest(t, "POST", "/api/superadmin/users", map[string]any{
		"name": "N", "email": "n@example.com", "password": "password123", "role_id": role.ID,
	}))
	if resp.StatusCode != fiber.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%v", resp.StatusCode, out)
	}
}

func TestCreateUserAPI_RoleIDBeyondActorCeiling_Rejected(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	destroyPerm := database.SAPermission{Module: "usuarios_central", Action: "destroy", Label: "Eliminar usuarios"}
	db.Create(&destroyPerm)
	role := database.SARole{Name: "Admin Completo"}
	db.Create(&role)
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: destroyPerm.ID})

	app := newAuthSATestApp(adminClaims(actor.ID, "usuarios_central.create")) // sin usuarios_central.destroy
	resp, out := doRequest(t, app, jsonRequest(t, "POST", "/api/superadmin/users", map[string]any{
		"name": "N", "email": "n2@example.com", "password": "password123", "role_id": role.ID,
	}))
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%v", resp.StatusCode, out)
	}

	var count int64
	db.Model(&database.SuperAdminUser{}).Where("email = ?", "n2@example.com").Count(&count)
	if count != 0 {
		t.Fatal("no debió crearse el usuario")
	}
}

// role=superadmin en el body es estructuralmente imposible: createUserRequest no tiene ese
// campo, así que ni siquiera un actor superadmin real puede colarlo por esta vía.
func TestCreateUserAPI_RoleFieldInBodyIsIgnored_EvenForSuperadminActor(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "root@example.com", "superadmin", 0)

	app := newAuthSATestApp(superadminClaims(actor.ID))
	resp, out := doRequest(t, app, jsonRequest(t, "POST", "/api/superadmin/users", map[string]any{
		"name": "N", "email": "n3@example.com", "password": "password123", "role": "superadmin",
	}))
	if resp.StatusCode != fiber.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%v", resp.StatusCode, out)
	}

	var reloaded database.SuperAdminUser
	db.Where("email = ?", "n3@example.com").First(&reloaded)
	if reloaded.Role != "admin" {
		t.Fatalf("Role = %q, want admin — POST /users NUNCA debe poder crear un superadmin", reloaded.Role)
	}
}

// ==================== system-role: un admin nunca lo alcanza (§10, §23) ====================

func TestChangeUserSystemRoleAPI_AdminActorRejected(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0) // Permissions=["*"] no importa, Role="admin"
	target := createAuthSAUser(t, db, "target@example.com", "admin", 0)

	app := newAuthSATestApp(adminClaims(actor.ID, "*"))
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d/system-role", target.ID),
		jsonBody(t, map[string]string{"role": "superadmin"}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (un admin nunca puede tocar system-role, ni con \"*\")", resp.StatusCode)
	}
}

// ==================== Cuenta protegida vía HTTP (§20, §21, §22) ====================

func TestResetUserPasswordAPI_ProtectedAccount_NoAuditOnRejection(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	target := createAuthSAUser(t, db, "sa@example.com", "superadmin", 0)

	app := newAuthSATestApp(adminClaims(actor.ID, "usuarios_central.reset_password"))
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/superadmin/users/%d/password", target.ID),
		jsonBody(t, map[string]string{"new_password": "newpassword123"}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	var count int64
	db.Model(&database.AuditLog{}).Where("action = ?", "user_password_reset").Count(&count)
	if count != 0 {
		t.Fatal("no debió auditarse un reset rechazado por cuenta protegida")
	}
}

func TestChangeUserStatusAPI_ProtectedAccount_NoAuditOnRejection(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	target := createAuthSAUser(t, db, "sa@example.com", "superadmin", 0)

	app := newAuthSATestApp(adminClaims(actor.ID, "usuarios_central.change_status"))
	active := false
	req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/superadmin/users/%d/status", target.ID),
		jsonBody(t, map[string]*bool{"active": &active}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	var count int64
	db.Model(&database.AuditLog{}).Where("action = ?", "user_status_changed").Count(&count)
	if count != 0 {
		t.Fatal("no debió auditarse un cambio de estado rechazado por cuenta protegida")
	}
}

func TestDestroyUserAPI_ProtectedAccount_NoAuditOnRejection(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	target := createAuthSAUser(t, db, "sa@example.com", "superadmin", 0)

	app := newAuthSATestApp(adminClaims(actor.ID, "usuarios_central.destroy"))
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/superadmin/users/%d", target.ID), nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	var count int64
	db.Model(&database.AuditLog{}).Where("action = ?", "user_deleted").Count(&count)
	if count != 0 {
		t.Fatal("no debió auditarse un destroy rechazado por cuenta protegida")
	}
	db.Model(&database.SuperAdminUser{}).Where("id = ? AND deleted_at IS NULL", target.ID).Count(&count)
	if count != 1 {
		t.Fatal("el superadmin objetivo no debió eliminarse")
	}
}

// ==================== Auditoría de operaciones exitosas (§24) ====================

func TestChangeUserRoleAPI_WritesAuditLog(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	target := createAuthSAUser(t, db, "target@example.com", "admin", 0)
	viewPerm := database.SAPermission{Module: "empresas", Action: "view", Label: "Ver empresas"}
	db.Create(&viewPerm)
	role := database.SARole{Name: "Soporte"}
	db.Create(&role)
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: viewPerm.ID})

	app := newAuthSATestApp(adminClaims(actor.ID, "usuarios_central.change_role", "empresas.view"))
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d/role", target.ID),
		jsonBody(t, map[string]uint{"role_id": role.ID}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var log database.AuditLog
	if err := db.Where("action = ? AND entity_id = ?", "user_role_changed", target.ID).First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
	if log.Payload == "" {
		t.Fatal("el payload no debería estar vacío")
	}
}

func TestDestroyUserAPI_WritesAuditLog(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	target := createAuthSAUser(t, db, "target@example.com", "admin", 0)

	app := newAuthSATestApp(adminClaims(actor.ID, "usuarios_central.destroy"))
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/superadmin/users/%d", target.ID), nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var log database.AuditLog
	if err := db.Where("action = ?", "user_deleted").First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
}

// El reset de contraseña nunca debe filtrar la contraseña al payload de auditoría.
func TestResetUserPasswordAPI_AuditLogNeverContainsPassword(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	target := createAuthSAUser(t, db, "target@example.com", "admin", 0)

	app := newAuthSATestApp(adminClaims(actor.ID, "usuarios_central.reset_password"))
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/superadmin/users/%d/password", target.ID),
		jsonBody(t, map[string]string{"new_password": "supersecretpassword"}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var log database.AuditLog
	if err := db.Where("action = ?", "user_password_reset").First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
	if strings.Contains(log.Payload, "supersecretpassword") {
		t.Fatal("el payload de auditoría NUNCA debe contener la contraseña")
	}
}

// ==================== Invalidación de sesión vía el middleware REAL (§21-§25) ====================

func TestChangeUserRoleAPI_InvalidatesOldJWT(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "superadmin", 0)
	target := createAuthSAUser(t, db, "target@example.com", "admin", 0)
	viewPerm := database.SAPermission{Module: "empresas", Action: "view", Label: "Ver empresas"}
	db.Create(&viewPerm)
	role := database.SARole{Name: "Soporte"}
	db.Create(&role)
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: viewPerm.ID})

	oldToken := mintTestSAToken(t, target.ID, "admin", 0)
	if got := statusThroughRealAuthMiddleware(t, oldToken); got != fiber.StatusOK {
		t.Fatalf("token antes del cambio: status = %d, want 200", got)
	}

	app := newAuthSATestApp(superadminClaims(actor.ID))
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d/role", target.ID),
		jsonBody(t, map[string]uint{"role_id": role.ID}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if got := statusThroughRealAuthMiddleware(t, oldToken); got != fiber.StatusUnauthorized {
		t.Fatalf("token tras el cambio de rol: status = %d, want 401", got)
	}
}

func TestChangeUserStatusAPI_DeactivateInvalidatesOldJWT(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	target := createAuthSAUser(t, db, "target@example.com", "admin", 0)

	oldToken := mintTestSAToken(t, target.ID, "admin", 0)
	if got := statusThroughRealAuthMiddleware(t, oldToken); got != fiber.StatusOK {
		t.Fatalf("token antes de desactivar: status = %d, want 200", got)
	}

	app := newAuthSATestApp(adminClaims(actor.ID, "usuarios_central.change_status"))
	active := false
	req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/superadmin/users/%d/status", target.ID),
		jsonBody(t, map[string]*bool{"active": &active}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if got := statusThroughRealAuthMiddleware(t, oldToken); got != fiber.StatusUnauthorized {
		t.Fatalf("token tras desactivar: status = %d, want 401 (Active=false, ver verifySuperAdminSession)", got)
	}
}

// Un usuario eliminado (soft-delete) tampoco puede seguir usando su JWT — First() de GORM excluye
// filas con deleted_at, así que verifySuperAdminSession lo trata igual que "no existe".
func TestDestroyUserAPI_DeletedUserCannotUseOldJWT(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	target := createAuthSAUser(t, db, "target@example.com", "admin", 0)

	oldToken := mintTestSAToken(t, target.ID, "admin", 0)
	if got := statusThroughRealAuthMiddleware(t, oldToken); got != fiber.StatusOK {
		t.Fatalf("token antes de eliminar: status = %d, want 200", got)
	}

	app := newAuthSATestApp(adminClaims(actor.ID, "usuarios_central.destroy"))
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/superadmin/users/%d", target.ID), nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if got := statusThroughRealAuthMiddleware(t, oldToken); got != fiber.StatusUnauthorized {
		t.Fatalf("token tras eliminar: status = %d, want 401", got)
	}
}

// ==================== Self escalation vía HTTP (§9, §23) ====================

func TestChangeUserRoleAPI_SelfCannotEscalate(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	self := createAuthSAUser(t, db, "self@example.com", "admin", 0)
	destroyPerm := database.SAPermission{Module: "usuarios_central", Action: "destroy", Label: "Eliminar usuarios"}
	db.Create(&destroyPerm)
	role := database.SARole{Name: "Superior"}
	db.Create(&role)
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: destroyPerm.ID})

	// El actor tiene change_role pero NO usuarios_central.destroy — el rol que intenta
	// asignarSE a sí mismo sí lo incluye.
	app := newAuthSATestApp(adminClaims(self.ID, "usuarios_central.change_role"))
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d/role", self.ID),
		jsonBody(t, map[string]uint{"role_id": role.ID}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (self role change no debe permitir escalar)", resp.StatusCode)
	}

	var reloaded database.SuperAdminUser
	db.First(&reloaded, self.ID)
	if reloaded.RoleID != nil {
		t.Fatal("RoleID no debió cambiar")
	}
}

// ==================== Cross-permission (§30, complemento de route_wiring_test.go) ====================

// ==================== Test crítico de delegación (§18, §28) — combinado end-to-end ====================
//
// Escenario textual del spec: Usuario A tiene roles.manage y usuarios_central.change_role, pero
// NO usuarios_central.reset_password. A intenta (1) agregar usuarios_central.reset_password a un
// rol existente — debe fallar (barrera 1, SARoleHandler.SetRolePermissionsAPI, Paso C). Luego
// intenta (2) asignar ESE MISMO rol (que quedó sin el permiso, porque el paso 1 fue rechazado) a
// un usuario B — también debe fallar, porque el rol simplemente nunca llegó a contenerlo. Esto
// prueba que la barrera 1 realmente sostiene la invariante: no hace falta llegar a la barrera 2
// para estar seguro (aunque la barrera 2 exista de forma independiente — ver
// TestSAUserService_ChangeRole_SecondBarrier_BlocksEvenIfRoleAlreadyExceedsCeiling en
// sa_user_service_test.go, que prueba la barrera 2 SOLA, sin depender de que la 1 haya fallado).
func TestCriticalDelegationScenario_RoleManageCannotEscalateViaPermissionEditThenAssign(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "admin", 0)
	targetUser := createAuthSAUser(t, db, "b@example.com", "admin", 0)
	resetPerm := database.SAPermission{Module: "usuarios_central", Action: "reset_password", Label: "Reset"}
	db.Create(&resetPerm)
	role := database.SARole{Name: "Rol X"}
	db.Create(&role)

	claims := adminClaims(actor.ID, "roles.manage", "usuarios_central.change_role")

	// Paso 1: A intenta agregar usuarios_central.reset_password al Rol X — vía SARoleHandler real.
	roleApp := fiber.New()
	roleH := NewSARoleHandler()
	roleApp.Put("/api/superadmin/roles/:id/permissions", func(c fiber.Ctx) error {
		c.Locals("sa_claims", claims)
		c.Locals("sa_user_id", claims.UserID)
		return c.Next()
	}, roleH.SetRolePermissionsAPI)

	req1 := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID),
		jsonBody(t, map[string][]uint{"permission_ids": {resetPerm.ID}}))
	req1.Header.Set("Content-Type", "application/json")
	resp1, err := roleApp.Test(req1)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp1.StatusCode != fiber.StatusForbidden {
		t.Fatalf("paso 1: status = %d, want 403 (A no puede delegar reset_password)", resp1.StatusCode)
	}

	// Paso 2: A intenta asignar el Rol X (que sigue vacío — el paso 1 fue rechazado) a B — vía
	// AuthSAHandler real. Debe funcionar (el rol vacío SÍ es delegable), pero B NO gana
	// reset_password porque el rol nunca lo tuvo.
	userApp := newAuthSATestApp(claims)
	req2 := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d/role", targetUser.ID),
		jsonBody(t, map[string]uint{"role_id": role.ID}))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := userApp.Test(req2)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp2.StatusCode != fiber.StatusOK {
		t.Fatalf("paso 2: status = %d, want 200 (rol vacío sí es delegable)", resp2.StatusCode)
	}

	// Verificación final: B NUNCA obtuvo usuarios_central.reset_password.
	keys := saPermissionsForUser(&database.SuperAdminUser{Role: "admin", RoleID: &role.ID})
	for _, k := range keys {
		if k == "usuarios_central.reset_password" {
			t.Fatal("B terminó con usuarios_central.reset_password — el escalamiento NO fue bloqueado")
		}
	}
}

func TestChangeUserRoleAPI_RequiresRoleIDField(t *testing.T) {
	setAuthSATestConfig(t)
	db := setupAuthSATestDB(t)
	actor := createAuthSAUser(t, db, "actor@example.com", "superadmin", 0)
	target := createAuthSAUser(t, db, "target@example.com", "admin", 0)

	app := newAuthSATestApp(superadminClaims(actor.ID))
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d/role", target.ID), jsonBody(t, map[string]any{}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (role_id requerido)", resp.StatusCode)
	}
}
