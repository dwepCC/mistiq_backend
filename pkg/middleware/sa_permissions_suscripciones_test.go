package middleware

// Fase 5 (etapa 3, Grupo 3 — suscripciones): suscripciones.create/update/change_status deben ser
// permisos completamente independientes entre sí y de suscripciones.view — ninguno tiene
// ".manage" en el catálogo, así que no hay ninguna expansión posible entre ellos (no se creó
// suscripciones.manage, tal como exigió el usuario). Reutiliza los helpers de
// jwt_superadmin_test.go / sa_permissions_test.go (mismo paquete).

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func newSuscripcionesPermTestApp(requiredPermission string) *fiber.App {
	app := fiber.New()
	app.Get("/api/superadmin/protegida", SuperAdminAuthAPI(), RequireSAPermission(requiredPermission), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusForSuscripcionesPerm(t *testing.T, requiredPermission, token string) int {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/superadmin/protegida", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := newSuscripcionesPermTestApp(requiredPermission).Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func tokenWithSuscripcionesPerms(t *testing.T, userID uint, perms []string) string {
	t.Helper()
	return mintSAToken(t, claimsWithPermissions(userID, "admin", 0, perms), testSAJWTSecret)
}

// ==================== Positivos ====================

func TestSuscripcionesPermissions_ExactGrantsAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)

	for _, perm := range []string{"suscripciones.view", "suscripciones.create", "suscripciones.update", "suscripciones.change_status"} {
		t.Run(perm, func(t *testing.T) {
			token := tokenWithSuscripcionesPerms(t, user.ID, []string{perm})
			if got := statusForSuscripcionesPerm(t, perm, token); got != fiber.StatusOK {
				t.Fatalf("status = %d, want 200", got)
			}
		})
	}
}

// ==================== Cruces negativos (punto 11 del enunciado) ====================

func TestSuscripcionesPermissions_ViewDoesNotGrantCreate(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithSuscripcionesPerms(t, user.ID, []string{"suscripciones.view"})

	if got := statusForSuscripcionesPerm(t, "suscripciones.create", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestSuscripcionesPermissions_ViewDoesNotGrantUpdate(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithSuscripcionesPerms(t, user.ID, []string{"suscripciones.view"})

	if got := statusForSuscripcionesPerm(t, "suscripciones.update", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestSuscripcionesPermissions_ViewDoesNotGrantChangeStatus(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithSuscripcionesPerms(t, user.ID, []string{"suscripciones.view"})

	if got := statusForSuscripcionesPerm(t, "suscripciones.change_status", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestSuscripcionesPermissions_CreateDoesNotImplyUpdate(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithSuscripcionesPerms(t, user.ID, []string{"suscripciones.create"})

	if got := statusForSuscripcionesPerm(t, "suscripciones.update", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestSuscripcionesPermissions_CreateDoesNotImplyChangeStatus(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithSuscripcionesPerms(t, user.ID, []string{"suscripciones.create"})

	if got := statusForSuscripcionesPerm(t, "suscripciones.change_status", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestSuscripcionesPermissions_UpdateDoesNotImplyChangeStatus(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithSuscripcionesPerms(t, user.ID, []string{"suscripciones.update"})

	if got := statusForSuscripcionesPerm(t, "suscripciones.change_status", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestSuscripcionesPermissions_ChangeStatusDoesNotImplyCreate(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithSuscripcionesPerms(t, user.ID, []string{"suscripciones.change_status"})

	if got := statusForSuscripcionesPerm(t, "suscripciones.create", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// Sin ningún permiso, ninguna escritura debe pasar.
func TestSuscripcionesPermissions_NoPermissions_NoWriteAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithSuscripcionesPerms(t, user.ID, []string{})

	for _, perm := range []string{"suscripciones.create", "suscripciones.update", "suscripciones.change_status"} {
		t.Run(perm, func(t *testing.T) {
			if got := statusForSuscripcionesPerm(t, perm, token); got != fiber.StatusForbidden {
				t.Fatalf("status = %d, want 403", got)
			}
		})
	}
}

// ==================== Superadmin ====================

func TestSuscripcionesPermissions_Superadmin_BypassButAuthStillEnforced(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)

	activeUser := createSAUser(t, db, "root@example.com", "superadmin", true, 0)
	for _, perm := range []string{"suscripciones.view", "suscripciones.create", "suscripciones.update", "suscripciones.change_status"} {
		token := mintSAToken(t, claimsWithPermissions(activeUser.ID, "superadmin", 0, nil), testSAJWTSecret)
		if got := statusForSuscripcionesPerm(t, perm, token); got != fiber.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (bypass superadmin)", perm, got)
		}
	}

	inactiveUser := createSAUser(t, db, "root2@example.com", "superadmin", false, 0)
	inactiveToken := mintSAToken(t, claimsWithPermissions(inactiveUser.ID, "superadmin", 0, nil), testSAJWTSecret)
	if got := statusForSuscripcionesPerm(t, "suscripciones.update", inactiveToken); got != fiber.StatusUnauthorized {
		t.Fatalf("Active=false: status = %d, want 401", got)
	}

	staleUser := createSAUser(t, db, "root3@example.com", "superadmin", true, 5)
	staleToken := mintSAToken(t, claimsWithPermissions(staleUser.ID, "superadmin", 1, nil), testSAJWTSecret)
	if got := statusForSuscripcionesPerm(t, "suscripciones.update", staleToken); got != fiber.StatusUnauthorized {
		t.Fatalf("TokenVersion inválida: status = %d, want 401", got)
	}
}
