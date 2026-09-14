package handler

// Fase 5 (etapa 3, Grupo 6 — fiscal): tests de requiredPermissionForFiscalAction (fuente única
// de verdad del mapeo action → permiso) y de la autorización real end-to-end en
// DocumentActionAPI/BulkActionAPI.
//
// Estrategia de test HTTP: en este entorno fiscaladmin.Enabled() es false (no hay
// FACTURADOR_BASE_URL configurado), así que cualquier request que SÍ pase la autorización llega
// a ensureConfigured() y recibe 503 — nunca 401/403. Eso permite probar "autorizado" sin mockear
// el servicio externo: basta confirmar que la respuesta NO es 401/403. Para "no autorizado", la
// respuesta debe ser 403 exacto — y como la comprobación de permiso corre ANTES de
// ensureConfigured (ver fiscal_handler.go), un 403 aquí prueba que la request nunca llegó a
// intentar proxyar nada hacia facturador_lycet.

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// ==================== Mapping puro ====================

func TestRequiredPermissionForFiscalAction_IndividualMapping(t *testing.T) {
	cases := []struct {
		action   string
		wantPerm string
		wantOK   bool
	}{
		{"retry", "fiscal.retry", true},
		{"send", "fiscal.retry", true},
		{"email", "fiscal.retry", true},
		{"poll", "fiscal.retry", true},
		{"cancel", "fiscal.cancel", true},
		{"force", "", false}, // pendiente, decisión confirmada con el usuario
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			perm, ok := requiredPermissionForFiscalAction(tc.action, false)
			if ok != tc.wantOK || perm != tc.wantPerm {
				t.Fatalf("requiredPermissionForFiscalAction(%q, false) = (%q, %v), want (%q, %v)",
					tc.action, perm, ok, tc.wantPerm, tc.wantOK)
			}
		})
	}
}

func TestRequiredPermissionForFiscalAction_BulkMapping(t *testing.T) {
	cases := []struct {
		action   string
		wantPerm string
		wantOK   bool
	}{
		{"send", "fiscal.bulk", true},
		{"retry", "fiscal.bulk", true},
		{"email", "fiscal.bulk", true},
		{"poll", "fiscal.bulk", true},
		{"force", "", false}, // pendiente también en bulk
		{"cancel", "", false}, // cancel no existe en bulk (no está en el whitelist del handler)
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			perm, ok := requiredPermissionForFiscalAction(tc.action, true)
			if ok != tc.wantOK || perm != tc.wantPerm {
				t.Fatalf("requiredPermissionForFiscalAction(%q, true) = (%q, %v), want (%q, %v)",
					tc.action, perm, ok, tc.wantPerm, tc.wantOK)
			}
		})
	}
}

// Acción desconocida: sin entrada en ningún mapeo (individual ni bulk) — rechazo, nunca un
// fallback permisivo.
func TestRequiredPermissionForFiscalAction_UnknownAction(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		_, ok := requiredPermissionForFiscalAction("whatever", bulk)
		if ok {
			t.Fatalf("bulk=%v: acción desconocida no debería resolver ningún permiso", bulk)
		}
	}
}

// ==================== HTTP real ====================

const fiscalTestJWTSecret = "fiscal-grupo6-test-secret"

func setupFiscalHandlerGrupo6DB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_journal_mode=WAL&_busy_timeout=15000", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.SuperAdminUser{}, &database.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })

	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{AppEnv: "development", SAJWTSecret: fiscalTestJWTSecret}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	return db
}

func newFiscalTestApp() *fiber.App {
	h := NewFiscalHandler()
	app := fiber.New()
	// Orden IDÉNTICO a internal/superadmin/routes.go: "documents/bulk/:action" (más específico)
	// debe registrarse ANTES que "documents/:uuid/:action" (comodín). Fiber resuelve por orden de
	// registro cuando hay ambigüedad entre un segmento estático y uno parametrizado en la misma
	// posición — invertir este orden hace que "/documents/bulk/retry" caiga en DocumentActionAPI
	// (uuid="bulk", action="retry") en vez de BulkActionAPI, rompiendo por completo la cobertura
	// de bulk. Ver routes.go líneas 189/191.
	app.Post("/api/superadmin/fiscal/documents/bulk/:action", middleware.SuperAdminAuthAPI(), h.BulkActionAPI)
	app.Post("/api/superadmin/fiscal/documents/:uuid/:action", middleware.SuperAdminAuthAPI(), h.DocumentActionAPI)
	return app
}

var fiscalTokenSeq int

func mintFiscalToken(t *testing.T, db *gorm.DB, role string, permissions []string) string {
	t.Helper()
	fiscalTokenSeq++
	user := database.SuperAdminUser{
		Name: "T", Email: fmt.Sprintf("%s-%d@example.com", role, fiscalTokenSeq), Role: role, Active: true,
	}
	if err := user.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	claims := &middleware.SuperAdminClaims{
		UserID: user.ID, Email: user.Email, Role: role, Type: "superadmin",
		TokenVersion: 0, Permissions: permissions, SAJWTVersion: middleware.CurrentSuperAdminJWTVersion(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte(fiscalTestJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func statusForFiscalAction(t *testing.T, app *fiber.App, method, path, token string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// retry: fiscal.retry → permitido (no 401/403; en este entorno de test da 503 porque
// fiscaladmin no está configurado, lo cual prueba que SÍ pasó la autorización).
func TestFiscalDocumentAction_RetryWithCorrectPermission_NotBlockedByAuthz(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "admin", []string{"fiscal.retry"})

	status := statusForFiscalAction(t, app, "POST", "/api/superadmin/fiscal/documents/abc-123/retry", token)
	if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
		t.Fatalf("status = %d, no debería ser 401/403 (fiscal.retry sí cubre retry)", status)
	}
}

// 16.1 fiscal.retry NO puede cancel.
func TestFiscalDocumentAction_RetryPermissionCannotCancel(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "admin", []string{"fiscal.retry"})

	status := statusForFiscalAction(t, app, "POST", "/api/superadmin/fiscal/documents/abc-123/cancel", token)
	if status != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (fiscal.retry no debe permitir cancel)", status)
	}
}

// 16.2 fiscal.cancel NO puede retry.
func TestFiscalDocumentAction_CancelPermissionCannotRetry(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "admin", []string{"fiscal.cancel"})

	status := statusForFiscalAction(t, app, "POST", "/api/superadmin/fiscal/documents/abc-123/retry", token)
	if status != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (fiscal.cancel no debe permitir retry)", status)
	}
}

// cancel: fiscal.cancel → permitido.
func TestFiscalDocumentAction_CancelWithCorrectPermission_NotBlockedByAuthz(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "admin", []string{"fiscal.cancel"})

	status := statusForFiscalAction(t, app, "POST", "/api/superadmin/fiscal/documents/abc-123/cancel", token)
	if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
		t.Fatalf("status = %d, no debería ser 401/403", status)
	}
}

// 16.3 fiscal.view no debe ejecutar NINGUNA acción de escritura (individual).
func TestFiscalDocumentAction_ViewCannotExecuteAnyWriteAction(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "admin", []string{"fiscal.view"})

	for _, action := range []string{"retry", "send", "email", "poll", "cancel"} {
		t.Run(action, func(t *testing.T) {
			status := statusForFiscalAction(t, app, "POST", "/api/superadmin/fiscal/documents/abc-123/"+action, token)
			if status != fiber.StatusForbidden {
				t.Fatalf("status = %d, want 403 (fiscal.view no debe permitir %s)", status, action)
			}
		})
	}
}

// send/email/poll también requieren fiscal.retry (no view, no ausencia de permiso).
func TestFiscalDocumentAction_SendEmailPollRequireFiscalRetry(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()

	for _, action := range []string{"send", "email", "poll"} {
		t.Run(action+"_sin_permiso", func(t *testing.T) {
			token := mintFiscalToken(t, db, "admin", []string{"fiscal.cancel"}) // permiso de otro tipo, no retry
			status := statusForFiscalAction(t, app, "POST", "/api/superadmin/fiscal/documents/abc-123/"+action, token)
			if status != fiber.StatusForbidden {
				t.Fatalf("status = %d, want 403 (%s requiere fiscal.retry)", status, action)
			}
		})
		t.Run(action+"_con_permiso", func(t *testing.T) {
			token := mintFiscalToken(t, db, "admin", []string{"fiscal.retry"})
			status := statusForFiscalAction(t, app, "POST", "/api/superadmin/fiscal/documents/abc-123/"+action, token)
			if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
				t.Fatalf("status = %d, no debería ser 401/403", status)
			}
		})
	}
}

// force: acción reconocida por el whitelist, pero sin permiso asignado (pendiente) — no debe
// dar 403 por falta de permiso (nadie tiene el permiso "adecuado" porque no existe todavía),
// pero SÍ debe comportarse igual que cualquier otra acción pendiente del rollout: alcanzable
// solo con autenticación, nunca bloqueada por RBAC ni abierta de más.
func TestFiscalDocumentAction_ForceIsPendingNotBlockedByMissingPermission(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "admin", []string{}) // sin ningún permiso fiscal

	status := statusForFiscalAction(t, app, "POST", "/api/superadmin/fiscal/documents/abc-123/force", token)
	if status == fiber.StatusForbidden {
		t.Fatalf("status = 403 inesperado — force está deliberadamente pendiente, sin gate adicional")
	}
	if status == fiber.StatusUnauthorized {
		t.Fatalf("status = 401 inesperado — el usuario está autenticado correctamente")
	}
}

// 4. Un action desconocido no obtiene ningún permiso — 400, incluso para un admin sin permisos
// (no es un tema de autorización, es de validación de la ruta).
func TestFiscalDocumentAction_UnknownActionRejected(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "admin", []string{"fiscal.retry", "fiscal.cancel"})

	status := statusForFiscalAction(t, app, "POST", "/api/superadmin/fiscal/documents/abc-123/whatever", token)
	if status != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (acción desconocida)", status)
	}
}

// 5. Manipular el action en la URL no permite escalar privilegios: un usuario con fiscal.retry
// no logra "cancel" cambiando la URL, ni con distintos casing/espacios.
func TestFiscalDocumentAction_CannotEscalateByManipulatingURLAction(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "admin", []string{"fiscal.retry"})

	for _, action := range []string{"cancel", "Cancel", "CANCEL"} {
		t.Run(action, func(t *testing.T) {
			status := statusForFiscalAction(t, app, "POST", "/api/superadmin/fiscal/documents/abc-123/"+action, token)
			if status != fiber.StatusForbidden && status != fiber.StatusBadRequest {
				t.Fatalf("status = %d, want 403 o 400 (nunca debe colarse un cancel)", status)
			}
		})
	}
}

// ==================== Bulk ====================

func TestFiscalBulkAction_CorrectPermission_NotBlockedByAuthz(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "admin", []string{"fiscal.bulk"})

	req := httptest.NewRequest("POST", "/api/superadmin/fiscal/documents/bulk/retry",
		strings.NewReader(`{"document_uuids":["abc-123"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == fiber.StatusUnauthorized || resp.StatusCode == fiber.StatusForbidden {
		t.Fatalf("status = %d, no debería ser 401/403 (fiscal.bulk cubre retry masivo)", resp.StatusCode)
	}
}

// Un permiso individual (fiscal.retry) NO alcanza para operaciones bulk — capacidades distintas.
func TestFiscalBulkAction_IndividualPermissionInsufficient(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "admin", []string{"fiscal.retry"})

	req := httptest.NewRequest("POST", "/api/superadmin/fiscal/documents/bulk/retry",
		strings.NewReader(`{"document_uuids":["abc-123"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (fiscal.retry individual no debe conceder bulk)", resp.StatusCode)
	}
}

func TestFiscalBulkAction_ViewInsufficient(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "admin", []string{"fiscal.view"})

	req := httptest.NewRequest("POST", "/api/superadmin/fiscal/documents/bulk/send",
		strings.NewReader(`{"document_uuids":["abc-123"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestFiscalBulkAction_UnknownActionRejected(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "admin", []string{"fiscal.bulk"})

	req := httptest.NewRequest("POST", "/api/superadmin/fiscal/documents/bulk/whatever",
		strings.NewReader(`{"document_uuids":["abc-123"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// ==================== Superadmin y seguridad de sesión ====================

func TestFiscalDocumentAction_Superadmin_Bypass(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()
	token := mintFiscalToken(t, db, "superadmin", nil)

	status := statusForFiscalAction(t, app, "POST", "/api/superadmin/fiscal/documents/abc-123/cancel", token)
	if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
		t.Fatalf("status = %d, superadmin debería tener bypass de autorización", status)
	}
}

// 9/19: superadmin con sesión inválida (Active=false) → 401, el bypass es solo de permisos.
func TestFiscalDocumentAction_Superadmin_InactiveSession_Rejected(t *testing.T) {
	db := setupFiscalHandlerGrupo6DB(t)
	app := newFiscalTestApp()

	user := database.SuperAdminUser{Name: "Root", Email: "root@example.com", Role: "superadmin", Active: true}
	if err := user.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&user).UpdateColumn("active", false).Error; err != nil {
		t.Fatal(err)
	}
	claims := &middleware.SuperAdminClaims{
		UserID: user.ID, Email: user.Email, Role: "superadmin", Type: "superadmin",
		TokenVersion: 0, Permissions: nil, SAJWTVersion: middleware.CurrentSuperAdminJWTVersion(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := tok.SignedString([]byte(fiscalTestJWTSecret))
	if err != nil {
		t.Fatal(err)
	}

	status := statusForFiscalAction(t, app, "POST", "/api/superadmin/fiscal/documents/abc-123/cancel", tokenString)
	if status != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (superadmin desactivado — bypass es solo de permisos, no de autenticación)", status)
	}
}
