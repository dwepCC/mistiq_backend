package middleware

// Tests de RequireSAPermission — la cadena completa SuperAdminAuthAPI() → RequireSAPermission(),
// exactamente como se registra en internal/superadmin/routes.go. Los helpers de fixture
// (setupSuperAdminAuthTestDB, setSuperAdminTestConfig, createSAUser, mintSAToken) están en
// jwt_superadmin_test.go, mismo paquete.

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

func claimsWithPermissions(userID uint, role string, tokenVersion uint, permissions []string) *SuperAdminClaims {
	return &SuperAdminClaims{
		UserID:       userID,
		Email:        "u@example.com",
		Role:         role,
		Type:         "superadmin",
		TokenVersion: tokenVersion,
		Permissions:  permissions,
		SAJWTVersion: CurrentSuperAdminJWTVersion(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
}

func newSAPermTestApp(requiredPermission string) *fiber.App {
	app := fiber.New()
	app.Get("/api/superadmin/protegida", SuperAdminAuthAPI(), RequireSAPermission(requiredPermission), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusForSAPermToken(t *testing.T, requiredPermission, token string) int {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/superadmin/protegida", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := newSAPermTestApp(requiredPermission).Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// tamperSAJWTPayload reescribe el payload de un JWT ya firmado (edita el campo `field` a `value`)
// SIN volver a firmarlo — la firma original queda pegada al payload nuevo, así que debe fallar
// la verificación. Simula un token editado a mano tras interceptarlo.
func tamperSAJWTPayload(t *testing.T, token string, field string, value any) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token con formato inesperado: %d partes", len(parts))
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	payload[field] = value
	newPayloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(newPayloadBytes)
	return strings.Join(parts, ".") // firma original, ahora inválida para este payload
}

// ==================== Superadmin ====================

// JWT válido + superadmin → permitido.
func TestRequireSAPermission_Superadmin_ValidToken_Allowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "superadmin", 0, []string{"*"}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "empresas.destroy", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

// superadmin sin permisos explícitos (Permissions=nil) → permitido por Role exacto.
func TestRequireSAPermission_Superadmin_NilPermissions_Allowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "superadmin", 0, nil), testSAJWTSecret)
	if got := statusForSAPermToken(t, "empresas.destroy", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 (bypass por Role, no depende de Permissions)", got)
	}
}

// superadmin con Permissions=[] → permitido por Role exacto (no por contenido de Permissions).
func TestRequireSAPermission_Superadmin_EmptyPermissions_Allowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "superadmin", 0, []string{}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "roles.manage", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

// superadmin con Active=false → rechazado (401, por SuperAdminAuthAPI — nunca llega a RequireSAPermission).
func TestRequireSAPermission_Superadmin_Inactive_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", false, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "superadmin", 0, []string{"*"}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "empresas.view", token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (Active=false — bypass de superadmin es solo de permisos)", got)
	}
}

// superadmin con TokenVersion incorrecta → rechazado (401).
func TestRequireSAPermission_Superadmin_StaleTokenVersion_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", true, 5)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "superadmin", 1, []string{"*"}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "empresas.view", token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (TokenVersion revocada)", got)
	}
}

// superadmin con JWT expirado → rechazado (401).
func TestRequireSAPermission_Superadmin_ExpiredToken_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", true, 0)

	claims := claimsWithPermissions(user.ID, "superadmin", 0, []string{"*"})
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-1 * time.Hour))
	token := mintSAToken(t, claims, testSAJWTSecret)
	if got := statusForSAPermToken(t, "empresas.view", token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (expirado)", got)
	}
}

// ==================== Usuario normal ====================

// permiso exacto → permitido.
func TestRequireSAPermission_NormalUser_ExactPermission_Allowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"empresas.view"}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "empresas.view", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

// permiso inexistente → 403.
func TestRequireSAPermission_NormalUser_MissingPermission_Forbidden(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"dashboard.view"}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "empresas.destroy", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// Permissions=[] → 403 (RoleID NULL / rol sin permisos — nunca acceso total).
func TestRequireSAPermission_NormalUser_EmptyPermissions_Forbidden(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "empresas.view", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (Permissions=[] no debe interpretarse como acceso total)", got)
	}
}

// permiso de otro módulo → 403 (no debe haber ninguna fuga entre módulos).
func TestRequireSAPermission_NormalUser_WrongModule_Forbidden(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"fiscal.view", "pagos.view"}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "empresas.view", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// ==================== Regla .manage ====================

// documentos.manage → concede documentos.view (acción normal del módulo).
func TestRequireSAPermission_Manage_GrantsNormalAction(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"documentos.manage"}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "documentos.view", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 (documentos.manage debe implicar documentos.view)", got)
	}
}

// documentos.manage NO concede documentos.approve_purchase (crítico, independiente).
func TestRequireSAPermission_Manage_DoesNotGrantCriticalAction(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"documentos.manage"}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "documentos.approve_purchase", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (documentos.manage NO debe implicar documentos.approve_purchase)", got)
	}
}

// roles.manage → concede roles.create (administración de roles, no está en la lista de críticos).
func TestRequireSAPermission_Manage_RolesGrantsCreate(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"roles.manage"}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "roles.create", token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

// Un módulo SIN ".manage" en el catálogo (migraciones) no tiene absolutamente nada que implicar:
// tener otro permiso de ese mismo módulo no concede una acción crítica no otorgada explícitamente.
func TestRequireSAPermission_NoManageForModule_CriticalActionStaysExplicit(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"migraciones.view", "migraciones.run"}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "migraciones.repair", token); got != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (migraciones.repair siempre explícito, no hay migraciones.manage)", got)
	}
}

// ==================== Seguridad ====================

// Modificar Permissions dentro del JWT sin volver a firmar → rechazado.
func TestRequireSAPermission_TamperedPermissions_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"dashboard.view"}), testSAJWTSecret)
	tampered := tamperSAJWTPayload(t, token, "permissions", []string{"*"})

	if got := statusForSAPermToken(t, "empresas.destroy", tampered); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (firma no coincide con el payload editado)", got)
	}
}

// Modificar Role dentro del JWT sin volver a firmar → rechazado.
func TestRequireSAPermission_TamperedRole_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"dashboard.view"}), testSAJWTSecret)
	tampered := tamperSAJWTPayload(t, token, "role", "superadmin")

	if got := statusForSAPermToken(t, "empresas.destroy", tampered); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (firma no coincide con el payload editado)", got)
	}
}

// Usuario (no superadmin) desactivado con JWT por lo demás válido → rechazado.
func TestRequireSAPermission_NormalUser_Inactive_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", false, 0)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 0, []string{"empresas.view"}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "empresas.view", token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (usuario desactivado)", got)
	}
}

// TokenVersion antigua (usuario normal) → rechazado.
func TestRequireSAPermission_NormalUser_StaleTokenVersion_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 3)

	token := mintSAToken(t, claimsWithPermissions(user.ID, "admin", 1, []string{"empresas.view"}), testSAJWTSecret)
	if got := statusForSAPermToken(t, "empresas.view", token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (TokenVersion revocada)", got)
	}
}
