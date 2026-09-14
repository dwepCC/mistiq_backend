package middleware

// Fase 5 (etapa 3, Grupo 4 — migraciones, ALTO RIESGO): migraciones.run/pause/resume/repair/
// backfill deben ser permisos completamente independientes entre sí y de migraciones.view —
// ninguno tiene ".manage" en el catálogo (no se creó migraciones.manage, tal como exigió el
// usuario). Reutiliza los helpers de jwt_superadmin_test.go / sa_permissions_test.go (mismo
// paquete).

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/gofiber/fiber/v3"
)

func newMigracionesPermTestApp(requiredPermission string) *fiber.App {
	app := fiber.New()
	app.Get("/api/superadmin/protegida", SuperAdminAuthAPI(), RequireSAPermission(requiredPermission), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusForMigracionesPerm(t *testing.T, requiredPermission, token string) int {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/superadmin/protegida", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := newMigracionesPermTestApp(requiredPermission).Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func tokenWithMigracionesPerms(t *testing.T, userID uint, perms []string) string {
	t.Helper()
	return mintSAToken(t, claimsWithPermissions(userID, "admin", 0, perms), testSAJWTSecret)
}

// ==================== Positivos — cada permiso exacto concede su propia acción ====================

func TestMigracionesPermissions_ExactGrantsAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)

	for _, perm := range []string{"migraciones.view", "migraciones.run", "migraciones.pause", "migraciones.resume", "migraciones.repair", "migraciones.backfill"} {
		t.Run(perm, func(t *testing.T) {
			token := tokenWithMigracionesPerms(t, user.ID, []string{perm})
			if got := statusForMigracionesPerm(t, perm, token); got != fiber.StatusOK {
				t.Fatalf("status = %d, want 200", got)
			}
		})
	}
}

// ==================== Punto 13: cruces negativos explícitos ====================

func TestMigracionesPermissions_ViewDoesNotGrantRun(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithMigracionesPerms(t, user.ID, []string{"migraciones.view"})

	if got := statusForMigracionesPerm(t, "migraciones.run", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestMigracionesPermissions_ViewDoesNotGrantRepair(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithMigracionesPerms(t, user.ID, []string{"migraciones.view"})

	if got := statusForMigracionesPerm(t, "migraciones.repair", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestMigracionesPermissions_ViewDoesNotGrantBackfill(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithMigracionesPerms(t, user.ID, []string{"migraciones.view"})

	if got := statusForMigracionesPerm(t, "migraciones.backfill", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestMigracionesPermissions_RunDoesNotGrantRepair(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithMigracionesPerms(t, user.ID, []string{"migraciones.run"})

	if got := statusForMigracionesPerm(t, "migraciones.repair", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestMigracionesPermissions_RunDoesNotGrantBackfill(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithMigracionesPerms(t, user.ID, []string{"migraciones.run"})

	if got := statusForMigracionesPerm(t, "migraciones.backfill", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestMigracionesPermissions_RepairDoesNotGrantRun(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithMigracionesPerms(t, user.ID, []string{"migraciones.repair"})

	if got := statusForMigracionesPerm(t, "migraciones.run", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestMigracionesPermissions_PauseDoesNotGrantRepair(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithMigracionesPerms(t, user.ID, []string{"migraciones.pause"})

	if got := statusForMigracionesPerm(t, "migraciones.repair", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestMigracionesPermissions_ResumeDoesNotGrantRepair(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithMigracionesPerms(t, user.ID, []string{"migraciones.resume"})

	if got := statusForMigracionesPerm(t, "migraciones.repair", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// Sin ningún permiso, ninguna escritura debe pasar.
func TestMigracionesPermissions_NoPermissions_NoWriteAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithMigracionesPerms(t, user.ID, []string{})

	for _, perm := range []string{"migraciones.run", "migraciones.pause", "migraciones.resume", "migraciones.repair", "migraciones.backfill"} {
		t.Run(perm, func(t *testing.T) {
			if got := statusForMigracionesPerm(t, perm, token); got != fiber.StatusForbidden {
				t.Fatalf("status = %d, want 403", got)
			}
		})
	}
}

// ==================== Superadmin ====================

func TestMigracionesPermissions_Superadmin_BypassButAuthStillEnforced(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)

	activeUser := createSAUser(t, db, "root@example.com", "superadmin", true, 0)
	for _, perm := range []string{"migraciones.view", "migraciones.run", "migraciones.pause", "migraciones.resume", "migraciones.repair", "migraciones.backfill"} {
		token := mintSAToken(t, claimsWithPermissions(activeUser.ID, "superadmin", 0, nil), testSAJWTSecret)
		if got := statusForMigracionesPerm(t, perm, token); got != fiber.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (bypass superadmin)", perm, got)
		}
	}

	inactiveUser := createSAUser(t, db, "root2@example.com", "superadmin", false, 0)
	inactiveToken := mintSAToken(t, claimsWithPermissions(inactiveUser.ID, "superadmin", 0, nil), testSAJWTSecret)
	if got := statusForMigracionesPerm(t, "migraciones.repair", inactiveToken); got != fiber.StatusUnauthorized {
		t.Fatalf("Active=false: status = %d, want 401", got)
	}

	staleUser := createSAUser(t, db, "root3@example.com", "superadmin", true, 5)
	staleToken := mintSAToken(t, claimsWithPermissions(staleUser.ID, "superadmin", 1, nil), testSAJWTSecret)
	if got := statusForMigracionesPerm(t, "migraciones.repair", staleToken); got != fiber.StatusUnauthorized {
		t.Fatalf("TokenVersion inválida: status = %d, want 401", got)
	}

	expiredClaims := claimsWithPermissions(activeUser.ID, "superadmin", 0, nil)
	expiredClaims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-1 * time.Hour))
	expiredToken := mintSAToken(t, expiredClaims, testSAJWTSecret)
	if got := statusForMigracionesPerm(t, "migraciones.repair", expiredToken); got != fiber.StatusUnauthorized {
		t.Fatalf("expirado: status = %d, want 401", got)
	}
}
