package middleware

// Fase 5 (etapa 3, Grupo 2 — pagos): pagos.approve/reject/refund deben ser permisos
// completamente independientes entre sí y de pagos.view — ninguno de los 4 tiene ".manage" en el
// catálogo, así que no hay ninguna expansión posible entre ellos (confirmado por estos tests).
// Reutiliza los helpers de jwt_superadmin_test.go / sa_permissions_test.go (mismo paquete).

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func newPagosPermTestApp(requiredPermission string) *fiber.App {
	app := fiber.New()
	app.Get("/api/superadmin/protegida", SuperAdminAuthAPI(), RequireSAPermission(requiredPermission), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusForPagosPerm(t *testing.T, requiredPermission, token string) int {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/superadmin/protegida", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := newPagosPermTestApp(requiredPermission).Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func tokenWithPagosPerms(t *testing.T, userID uint, perms []string) string {
	t.Helper()
	return mintSAToken(t, claimsWithPermissions(userID, "admin", 0, perms), testSAJWTSecret)
}

// 1. pagos.view NO permite approve.
func TestPagosPermissions_ViewDoesNotGrantApprove(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPagosPerms(t, user.ID, []string{"pagos.view"})

	if got := statusForPagosPerm(t, "pagos.approve", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// 2. pagos.view NO permite reject.
func TestPagosPermissions_ViewDoesNotGrantReject(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPagosPerms(t, user.ID, []string{"pagos.view"})

	if got := statusForPagosPerm(t, "pagos.reject", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// 3. pagos.view NO permite refund.
func TestPagosPermissions_ViewDoesNotGrantRefund(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPagosPerms(t, user.ID, []string{"pagos.view"})

	if got := statusForPagosPerm(t, "pagos.refund", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// pagos.view SÍ permite consultar (control positivo — no es solo una lista de negativos).
func TestPagosPermissions_ViewGrantsView(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPagosPerms(t, user.ID, []string{"pagos.view"})

	if got := statusForPagosPerm(t, "pagos.view", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

// 4. pagos.approve NO permite reject si no tiene también pagos.reject.
func TestPagosPermissions_ApproveDoesNotGrantReject(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPagosPerms(t, user.ID, []string{"pagos.approve"})

	if got := statusForPagosPerm(t, "pagos.reject", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// 5. pagos.approve NO permite refund si no tiene también pagos.refund.
func TestPagosPermissions_ApproveDoesNotGrantRefund(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPagosPerms(t, user.ID, []string{"pagos.approve"})

	if got := statusForPagosPerm(t, "pagos.refund", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// 6. pagos.reject NO permite refund.
func TestPagosPermissions_RejectDoesNotGrantRefund(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPagosPerms(t, user.ID, []string{"pagos.reject"})

	if got := statusForPagosPerm(t, "pagos.refund", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// Positivos: cada permiso exacto SÍ concede su propia acción.
func TestPagosPermissions_ExactGrantsAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)

	cases := []string{"pagos.approve", "pagos.reject", "pagos.refund"}
	for _, perm := range cases {
		t.Run(perm, func(t *testing.T) {
			token := tokenWithPagosPerms(t, user.ID, []string{perm})
			if got := statusForPagosPerm(t, perm, token); got != fiber.StatusOK {
				t.Fatalf("status = %d, want 200", got)
			}
		})
	}
}

// 7. Un usuario sin ningún permiso de pagos no puede ejecutar ninguna escritura.
func TestPagosPermissions_NoPermissions_NoWriteAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithPagosPerms(t, user.ID, []string{})

	for _, perm := range []string{"pagos.approve", "pagos.reject", "pagos.refund"} {
		t.Run(perm, func(t *testing.T) {
			if got := statusForPagosPerm(t, perm, token); got != fiber.StatusForbidden {
				t.Fatalf("status = %d, want 403", got)
			}
		})
	}
}

// Superadmin: bypass total sobre los 4 permisos de pagos, pero Active/TokenVersion se siguen
// exigiendo igual (no hay bypass de autenticación).
func TestPagosPermissions_Superadmin_BypassButAuthStillEnforced(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)

	activeUser := createSAUser(t, db, "root@example.com", "superadmin", true, 0)
	for _, perm := range []string{"pagos.view", "pagos.approve", "pagos.reject", "pagos.refund"} {
		token := mintSAToken(t, claimsWithPermissions(activeUser.ID, "superadmin", 0, nil), testSAJWTSecret)
		if got := statusForPagosPerm(t, perm, token); got != fiber.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (bypass superadmin)", perm, got)
		}
	}

	inactiveUser := createSAUser(t, db, "root2@example.com", "superadmin", false, 0)
	inactiveToken := mintSAToken(t, claimsWithPermissions(inactiveUser.ID, "superadmin", 0, nil), testSAJWTSecret)
	if got := statusForPagosPerm(t, "pagos.approve", inactiveToken); got != fiber.StatusUnauthorized {
		t.Fatalf("Active=false: status = %d, want 401", got)
	}

	staleUser := createSAUser(t, db, "root3@example.com", "superadmin", true, 5)
	staleToken := mintSAToken(t, claimsWithPermissions(staleUser.ID, "superadmin", 1, nil), testSAJWTSecret)
	if got := statusForPagosPerm(t, "pagos.approve", staleToken); got != fiber.StatusUnauthorized {
		t.Fatalf("TokenVersion inválida: status = %d, want 401", got)
	}
}

// 8. Manipular Permissions sin modificar la firma → 401 (falla la firma, no llega a evaluarse el
// permiso).
func TestPagosPermissions_TamperedPermissions_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)

	token := tokenWithPagosPerms(t, user.ID, []string{"pagos.view"})
	tampered := tamperSAJWTPayload(t, token, "permissions", []string{"pagos.approve", "pagos.reject", "pagos.refund"})

	if got := statusForPagosPerm(t, "pagos.refund", tampered); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (firma no coincide con el payload editado)", got)
	}
}

// 9. TokenVersion antigua → 401 (usuario normal, no solo superadmin).
func TestPagosPermissions_StaleTokenVersion_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 3)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 1, []string{"pagos.approve", "pagos.reject", "pagos.refund"}), testSAJWTSecret)
	if got := statusForPagosPerm(t, "pagos.approve", token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", got)
	}
}
