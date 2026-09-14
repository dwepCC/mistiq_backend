package middleware

// Fase 5 (etapa 3, Grupo 1 — empresas/tenants): tests de RequireSuperAdminOnly (destroy-complete,
// operations-key) y de empresas.master_access como permiso real y otorgable (no bypass-only).
// Reutiliza los helpers de jwt_superadmin_test.go / sa_permissions_test.go (mismo paquete):
// setSuperAdminTestConfig, setupSuperAdminAuthTestDB, createSAUser, mintSAToken,
// claimsWithPermissions.

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func newSuperAdminOnlyTestApp() *fiber.App {
	app := fiber.New()
	app.Get("/api/superadmin/protegida", SuperAdminAuthAPI(), RequireSuperAdminOnly(), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusForSuperAdminOnly(t *testing.T, token string) int {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/superadmin/protegida", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := newSuperAdminOnlyTestApp().Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// destroy-complete / operations-key → admin normal rechazado, incluso con Permissions=["*"]
// (RequireSuperAdminOnly no depende del contenido de Permissions, solo de Role exacto).
func TestRequireSuperAdminOnly_NormalAdmin_RejectedEvenWithWildcardPermissions(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"*"}), testSAJWTSecret)
	if got := statusForSuperAdminOnly(t, token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (RequireSuperAdminOnly no es otorgable vía permisos)", got)
	}
}

// destroy-complete / operations-key → superadmin permitido.
func TestRequireSuperAdminOnly_Superadmin_Allowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "superadmin", 0, nil), testSAJWTSecret)
	if got := statusForSuperAdminOnly(t, token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

// superadmin con Active=false → 401 (no 403 — la sesión ya es inválida antes de llegar a
// RequireSuperAdminOnly).
func TestRequireSuperAdminOnly_Superadmin_Inactive_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", false, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "superadmin", 0, nil), testSAJWTSecret)
	if got := statusForSuperAdminOnly(t, token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", got)
	}
}

// superadmin con TokenVersion revocada → 401.
func TestRequireSuperAdminOnly_Superadmin_StaleTokenVersion_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", true, 5)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "superadmin", 1, nil), testSAJWTSecret)
	if got := statusForSuperAdminOnly(t, token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", got)
	}
}

// ==================== empresas.master_access: permiso real, no bypass-only ====================

func newMasterAccessPermTestApp() *fiber.App {
	app := fiber.New()
	app.Get("/api/superadmin/protegida", SuperAdminAuthAPI(), RequireSAPermission("empresas.master_access"), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusForMasterAccessPerm(t *testing.T, token string) int {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/superadmin/protegida", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := newMasterAccessPermTestApp().Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// Un admin con exactamente empresas.master_access asignado → permitido (permiso real, otorgable).
func TestMasterAccessPermission_ExactGrant_Allowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"empresas.master_access"}), testSAJWTSecret)
	if got := statusForMasterAccessPerm(t, token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

// empresas.update NO debe otorgar master_access — son permisos completamente separados.
func TestMasterAccessPermission_UpdateAloneDoesNotGrantIt(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"empresas.update", "empresas.view", "empresas.create", "empresas.change_status"}), testSAJWTSecret)
	if got := statusForMasterAccessPerm(t, token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (empresas.update/view/create/change_status no implican master_access)", got)
	}
}

// Un "empresas.manage" hipotético (no existe en el catálogo real) tampoco debe implicar
// master_access: el módulo "empresas" no tiene entrada en saManageImpliedActions.
func TestMasterAccessPermission_HypotheticalManageDoesNotGrantIt(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"empresas.manage"}), testSAJWTSecret)
	if got := statusForMasterAccessPerm(t, token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// superadmin sigue con bypass total, además del permiso otorgable.
func TestMasterAccessPermission_Superadmin_Allowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "superadmin", 0, nil), testSAJWTSecret)
	if got := statusForMasterAccessPerm(t, token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}
