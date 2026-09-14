package middleware

// Fase 5 (etapa 3, Grupo 5, Parte B — documentos): documentos.manage implica ÚNICAMENTE
// documentos.view (allowlist ya vigente desde la Fase 5 etapa 1, ver saManageImpliedActions en
// sa_permissions.go) — NUNCA documentos.approve_purchase, que es un permiso independiente con
// efecto financiero real (acredita documentos pagados). Reutiliza los helpers de
// jwt_superadmin_test.go / sa_permissions_test.go (mismo paquete).

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func newDocumentosPermTestApp(requiredPermission string) *fiber.App {
	app := fiber.New()
	app.Get("/api/superadmin/protegida", SuperAdminAuthAPI(), RequireSAPermission(requiredPermission), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusForDocumentosPerm(t *testing.T, requiredPermission, token string) int {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/superadmin/protegida", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := newDocumentosPermTestApp(requiredPermission).Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func tokenWithDocumentosPerms(t *testing.T, userID uint, perms []string) string {
	t.Helper()
	return mintSAToken(t, claimsWithPermissions(userID, "admin", 0, perms), testSAJWTSecret)
}

// 16. manage → documentos.view (implicado por la allowlist).
func TestDocumentosPermissions_ManageGrantsView(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithDocumentosPerms(t, user.ID, []string{"documentos.manage"})

	if got := statusForDocumentosPerm(t, "documentos.view", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 (documentos.manage debe implicar documentos.view)", got)
	}
}

// manage → NO approve_purchase (la separación central de este grupo).
func TestDocumentosPermissions_ManageDoesNotGrantApprovePurchase(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithDocumentosPerms(t, user.ID, []string{"documentos.manage"})

	if got := statusForDocumentosPerm(t, "documentos.approve_purchase", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (documentos.manage NO debe implicar approve_purchase)", got)
	}
}

// approve_purchase: permitido con el permiso exacto.
func TestDocumentosPermissions_ApprovePurchaseAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithDocumentosPerms(t, user.ID, []string{"documentos.approve_purchase"})

	if got := statusForDocumentosPerm(t, "documentos.approve_purchase", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

func TestDocumentosPermissions_ViewDoesNotGrantApprovePurchase(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithDocumentosPerms(t, user.ID, []string{"documentos.view"})

	if got := statusForDocumentosPerm(t, "documentos.approve_purchase", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestDocumentosPermissions_ViewDoesNotGrantManage(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithDocumentosPerms(t, user.ID, []string{"documentos.view"})

	if got := statusForDocumentosPerm(t, "documentos.manage", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// Sin ningún permiso, ninguna escritura debe pasar.
func TestDocumentosPermissions_NoPermissions_NoWriteAllowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "u@example.com", "admin", true, 0)
	token := tokenWithDocumentosPerms(t, user.ID, []string{})

	for _, perm := range []string{"documentos.manage", "documentos.approve_purchase"} {
		t.Run(perm, func(t *testing.T) {
			if got := statusForDocumentosPerm(t, perm, token); got != fiber.StatusForbidden {
				t.Fatalf("status = %d, want 403", got)
			}
		})
	}
}

// 17. Superadmin.
func TestDocumentosPermissions_Superadmin_BypassButAuthStillEnforced(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)

	activeUser := createSAUser(t, db, "root@example.com", "superadmin", true, 0)
	for _, perm := range []string{"documentos.view", "documentos.manage", "documentos.approve_purchase"} {
		token := mintSAToken(t, claimsWithPermissions(activeUser.ID, "superadmin", 0, nil), testSAJWTSecret)
		if got := statusForDocumentosPerm(t, perm, token); got != fiber.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (bypass superadmin)", perm, got)
		}
	}

	inactiveUser := createSAUser(t, db, "root2@example.com", "superadmin", false, 0)
	inactiveToken := mintSAToken(t, claimsWithPermissions(inactiveUser.ID, "superadmin", 0, nil), testSAJWTSecret)
	if got := statusForDocumentosPerm(t, "documentos.approve_purchase", inactiveToken); got != fiber.StatusUnauthorized {
		t.Fatalf("Active=false: status = %d, want 401", got)
	}

	staleUser := createSAUser(t, db, "root3@example.com", "superadmin", true, 5)
	staleToken := mintSAToken(t, claimsWithPermissions(staleUser.ID, "superadmin", 1, nil), testSAJWTSecret)
	if got := statusForDocumentosPerm(t, "documentos.approve_purchase", staleToken); got != fiber.StatusUnauthorized {
		t.Fatalf("TokenVersion inválida: status = %d, want 401", got)
	}
}
