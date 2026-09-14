package middleware

// Fase 5 (etapa 3, Grupo 5, Parte A — planes): planes.create/update/change_status/destroy deben
// ser permisos completamente independientes entre sí y de planes.view — ninguno tiene ".manage"
// en el catálogo (no se creó planes.manage). Reutiliza los helpers de jwt_superadmin_test.go /
// sa_permissions_test.go (mismo paquete).

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func newPlanesPermTestApp(requiredPermission string) *fiber.App {
	app := fiber.New()
	app.Get("/api/superadmin/protegida", SuperAdminAuthAPI(), RequireSAPermission(requiredPermission), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusForPlanesPerm(t *testing.T, requiredPermission, token string) int {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/superadmin/protegida", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := newPlanesPermTestApp(requiredPermission).Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func tokenWithPlanesPerms(t *testing.T, userID uint, perms []string) string {
	t.Helper()
	return mintSAToken(t, claimsWithPermissions(userID, "admin", 0, perms), testSAJWTSecret)
}

// 15. Tests de planes — create.
func TestPlanesPermissions_CreateAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPlanesPerms(t, user.ID, []string{"planes.create"})

	if got := statusForPlanesPerm(t, "planes.create", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

func TestPlanesPermissions_ViewDoesNotGrantCreate(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPlanesPerms(t, user.ID, []string{"planes.view"})

	if got := statusForPlanesPerm(t, "planes.create", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// update.
func TestPlanesPermissions_UpdateAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPlanesPerms(t, user.ID, []string{"planes.update"})

	if got := statusForPlanesPerm(t, "planes.update", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

func TestPlanesPermissions_ViewDoesNotGrantUpdate(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPlanesPerms(t, user.ID, []string{"planes.view"})

	if got := statusForPlanesPerm(t, "planes.update", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// change_status.
func TestPlanesPermissions_ChangeStatusAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPlanesPerms(t, user.ID, []string{"planes.change_status"})

	if got := statusForPlanesPerm(t, "planes.change_status", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

func TestPlanesPermissions_ViewDoesNotGrantChangeStatus(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPlanesPerms(t, user.ID, []string{"planes.view"})

	if got := statusForPlanesPerm(t, "planes.change_status", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// planes.update NO implica change_status — permisos independientes.
func TestPlanesPermissions_UpdateDoesNotGrantChangeStatus(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPlanesPerms(t, user.ID, []string{"planes.update"})

	if got := statusForPlanesPerm(t, "planes.change_status", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// destroy: permiso explícito, ningún otro permiso lo concede.
func TestPlanesPermissions_DestroyAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPlanesPerms(t, user.ID, []string{"planes.destroy"})

	if got := statusForPlanesPerm(t, "planes.destroy", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

func TestPlanesPermissions_DestroyNotGrantedByAnyOtherPermission(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)

	for _, perm := range []string{"planes.view", "planes.create", "planes.update", "planes.change_status"} {
		t.Run(perm, func(t *testing.T) {
			token := tokenWithPlanesPerms(t, user.ID, []string{perm})
			if got := statusForPlanesPerm(t, "planes.destroy", token); got != fiber.StatusForbidden {
				t.Fatalf("status = %d, want 403 (%q no debe otorgar destroy)", got, perm)
			}
		})
	}
	// Tampoco los cuatro juntos (simula un rol amplio como Admin) otorgan destroy sin el permiso
	// explícito.
	all := tokenWithPlanesPerms(t, user.ID, []string{"planes.view", "planes.create", "planes.update", "planes.change_status"})
	if got := statusForPlanesPerm(t, "planes.destroy", all); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (los otros 4 permisos juntos no deben otorgar destroy)", got)
	}
}

// Sin ningún permiso, ninguna escritura debe pasar.
func TestPlanesPermissions_NoPermissions_NoWriteAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPlanesPerms(t, user.ID, []string{})

	for _, perm := range []string{"planes.create", "planes.update", "planes.change_status", "planes.destroy"} {
		t.Run(perm, func(t *testing.T) {
			if got := statusForPlanesPerm(t, perm, token); got != fiber.StatusForbidden {
				t.Fatalf("status = %d, want 403", got)
			}
		})
	}
}

// 17. Superadmin.
func TestPlanesPermissions_Superadmin_BypassButAuthStillEnforced(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)

	activeUser := createSAUser(t, db, "root@example.com", "superadmin", true, 0)
	for _, perm := range []string{"planes.view", "planes.create", "planes.update", "planes.change_status", "planes.destroy"} {
		token := mintSAToken(t, claimsWithPermissions(activeUser.ID, "superadmin", 0, nil), testSAJWTSecret)
		if got := statusForPlanesPerm(t, perm, token); got != fiber.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (bypass superadmin)", perm, got)
		}
	}

	inactiveUser := createSAUser(t, db, "root2@example.com", "superadmin", false, 0)
	inactiveToken := mintSAToken(t, claimsWithPermissions(inactiveUser.ID, "superadmin", 0, nil), testSAJWTSecret)
	if got := statusForPlanesPerm(t, "planes.destroy", inactiveToken); got != fiber.StatusUnauthorized {
		t.Fatalf("Active=false: status = %d, want 401", got)
	}

	staleUser := createSAUser(t, db, "root3@example.com", "superadmin", true, 5)
	staleToken := mintSAToken(t, claimsWithPermissions(staleUser.ID, "superadmin", 1, nil), testSAJWTSecret)
	if got := statusForPlanesPerm(t, "planes.destroy", staleToken); got != fiber.StatusUnauthorized {
		t.Fatalf("TokenVersion inválida: status = %d, want 401", got)
	}
}
