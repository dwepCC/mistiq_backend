package middleware

import (
	"fmt"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

const testSAJWTSecret = "test-sa-secret"

// setupSuperAdminAuthTestDB monta un SuperAdminUser en sqlite en memoria y lo conecta como
// database.CentralDB — verifySuperAdminSession consulta ese global, igual que en producción.
func setupSuperAdminAuthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.SuperAdminUser{}); err != nil {
		t.Fatal(err)
	}
	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })
	return db
}

// setSuperAdminTestConfig fija config.AppConfig para el test (secret + entorno) y lo restaura al
// terminar — mismo patrón que billing_reissue_test.go (save/restore explícito), necesario porque
// TestMain de este paquete ya deja un config.AppConfig por defecto compartido entre tests.
func setSuperAdminTestConfig(t *testing.T, prod bool) {
	t.Helper()
	prev := config.AppConfig
	env := "development"
	if prod {
		env = "production"
	}
	config.AppConfig = &config.Config{AppEnv: env, SAJWTSecret: testSAJWTSecret}
	t.Cleanup(func() { config.AppConfig = prev })
}

func newSuperAdminAuthTestApp() *fiber.App {
	app := fiber.New()
	app.Get("/api/superadmin/protegida", SuperAdminAuthAPI(), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func mintSAToken(t *testing.T, claims *SuperAdminClaims, secret string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func createSAUser(t *testing.T, db *gorm.DB, email, role string, active bool, tokenVersion uint) database.SuperAdminUser {
	t.Helper()
	u := database.SuperAdminUser{Name: "Test User", Email: email, Role: role, Active: active, TokenVersion: tokenVersion}
	if err := u.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	// GORM omite un valor `false` en el INSERT para una columna `gorm:"default:true"` (lo trata
	// como "no provisto" y deja que la BD aplique su default) — forzamos el valor real con un
	// UPDATE explícito para no depender de ese comportamiento al construir el fixture del test.
	if err := db.Model(&u).UpdateColumn("active", active).Error; err != nil {
		t.Fatal(err)
	}
	u.Active = active
	return u
}

func validClaims(userID uint, role string, tokenVersion uint) *SuperAdminClaims {
	return &SuperAdminClaims{
		UserID:       userID,
		Email:        "u@example.com",
		Role:         role,
		Type:         "superadmin",
		TokenVersion: tokenVersion,
		Permissions:  []string{"*"},
		SAJWTVersion: CurrentSuperAdminJWTVersion(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
}

func statusForSAToken(t *testing.T, token string) int {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/superadmin/protegida", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := newSuperAdminAuthTestApp().Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// ==================== Autenticación ====================

// 1. JWT válido + usuario activo → permitido.
func TestSuperAdminAuth_ValidTokenActiveUser_Allowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, validClaims(user.ID, "admin", 0), testSAJWTSecret)
	if got := statusForSAToken(t, token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

// 2. JWT expirado → rechazado.
func TestSuperAdminAuth_ExpiredToken_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	claims := validClaims(user.ID, "admin", 0)
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-1 * time.Hour))
	token := mintSAToken(t, claims, testSAJWTSecret)
	if got := statusForSAToken(t, token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (token expirado)", got)
	}
}

// 3. Firma inválida → rechazado.
func TestSuperAdminAuth_InvalidSignature_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	token := mintSAToken(t, validClaims(user.ID, "admin", 0), "secret-equivocado")
	if got := statusForSAToken(t, token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (firma inválida)", got)
	}
}

// 4. Usuario inexistente → rechazado.
func TestSuperAdminAuth_NonExistentUser_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	setupSuperAdminAuthTestDB(t) // BD vacía, sin usuarios

	token := mintSAToken(t, validClaims(99999, "admin", 0), testSAJWTSecret)
	if got := statusForSAToken(t, token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (usuario inexistente)", got)
	}
}

// 5. Usuario soft-deleted → rechazado.
func TestSuperAdminAuth_SoftDeletedUser_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)
	if err := db.Delete(&user).Error; err != nil { // soft-delete (DeletedAt)
		t.Fatal(err)
	}

	token := mintSAToken(t, validClaims(user.ID, "admin", 0), testSAJWTSecret)
	if got := statusForSAToken(t, token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (usuario eliminado)", got)
	}
}

// 6. Usuario Active=false → rechazado.
func TestSuperAdminAuth_InactiveUser_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", false, 0)

	token := mintSAToken(t, validClaims(user.ID, "admin", 0), testSAJWTSecret)
	if got := statusForSAToken(t, token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (usuario desactivado)", got)
	}
}

// ==================== TokenVersion ====================

// 7. JWT version=1 + DB version=1 → permitido.
func TestSuperAdminAuth_TokenVersionMatches_Allowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 1)

	token := mintSAToken(t, validClaims(user.ID, "admin", 1), testSAJWTSecret)
	if got := statusForSAToken(t, token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

// 8. JWT version=1 + DB version=2 → rechazado.
func TestSuperAdminAuth_TokenVersionMismatch_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 2)

	token := mintSAToken(t, validClaims(user.ID, "admin", 1), testSAJWTSecret)
	if got := statusForSAToken(t, token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (token_version desactualizada)", got)
	}
}

// 9. Incrementar TokenVersion invalida tokens anteriores.
func TestSuperAdminAuth_IncrementTokenVersion_InvalidatesOldToken(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	oldToken := mintSAToken(t, validClaims(user.ID, "admin", 0), testSAJWTSecret)
	if got := statusForSAToken(t, oldToken); got != fiber.StatusOK {
		t.Fatalf("token antes de invalidar: status = %d, want 200", got)
	}

	if err := user.IncrementTokenVersion(db); err != nil {
		t.Fatalf("IncrementTokenVersion: %v", err)
	}

	if got := statusForSAToken(t, oldToken); got != fiber.StatusUnauthorized {
		t.Fatalf("token tras invalidar: status = %d, want 401", got)
	}
}

// 10. Nuevo login (simulado) genera la nueva versión y esa sí es válida.
func TestSuperAdminAuth_NewTokenAfterInvalidation_Allowed(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	if err := user.IncrementTokenVersion(db); err != nil {
		t.Fatalf("IncrementTokenVersion: %v", err)
	}
	// user.TokenVersion se actualiza en memoria dentro de IncrementTokenVersion — así es como un
	// nuevo login (que relee el usuario) obtendría la versión vigente.
	newToken := mintSAToken(t, validClaims(user.ID, "admin", user.TokenVersion), testSAJWTSecret)
	if got := statusForSAToken(t, newToken); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 (token nuevo con la versión vigente)", got)
	}
}

// ==================== Superadmin ====================

// 17. Superadmin mantiene bypass de permisos (Permissions=["*"] pasa igual que cualquier otro,
// la validación de sesión no depende del valor de Role — se prueba junto con 18/19/20).
func TestSuperAdminAuth_Superadmin_PermissionsWildcard_ButAuthChecksStillApply(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", true, 0)

	token := mintSAToken(t, validClaims(user.ID, "superadmin", 0), testSAJWTSecret)
	if got := statusForSAToken(t, token); got != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 (superadmin activo, sesión válida)", got)
	}
}

// 18. Superadmin Active=false → rechazado (el bypass es de permisos, NUNCA de autenticación).
func TestSuperAdminAuth_Superadmin_Inactive_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", false, 0)

	token := mintSAToken(t, validClaims(user.ID, "superadmin", 0), testSAJWTSecret)
	if got := statusForSAToken(t, token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (superadmin desactivado no debe tener bypass de autenticación)", got)
	}
}

// 19. Superadmin soft-deleted → rechazado.
func TestSuperAdminAuth_Superadmin_SoftDeleted_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", true, 0)
	if err := db.Delete(&user).Error; err != nil {
		t.Fatal(err)
	}

	token := mintSAToken(t, validClaims(user.ID, "superadmin", 0), testSAJWTSecret)
	if got := statusForSAToken(t, token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (superadmin eliminado no debe tener bypass de autenticación)", got)
	}
}

// 20. Superadmin con TokenVersion antigua → rechazado.
func TestSuperAdminAuth_Superadmin_StaleTokenVersion_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "root@example.com", "superadmin", true, 5)

	token := mintSAToken(t, validClaims(user.ID, "superadmin", 1), testSAJWTSecret)
	if got := statusForSAToken(t, token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (superadmin con sesión revocada no debe tener bypass de autenticación)", got)
	}
}

// ==================== Seguridad ====================

// 21. Manipular "role" dentro del JWT no debe permitir convertirse en superadmin si la firma no
// es válida: se firma con un secreto distinto al configurado en el middleware (simula un token
// re-firmado tras editar el payload a mano) y debe rechazarse igual que cualquier firma inválida.
func TestSuperAdminAuth_TamperedRoleWithInvalidSignature_Rejected(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)
	// El usuario real es "admin", no superadmin.
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	tampered := validClaims(user.ID, "superadmin", 0) // intento de escalar el claim
	token := mintSAToken(t, tampered, "secreto-del-atacante")
	if got := statusForSAToken(t, token); got != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (firma no coincide con el secreto del servidor)", got)
	}
}

// 22. Un JWT válido (firma y expiración correctas) no debe permitir saltarse Active/DeletedAt/
// TokenVersion — combinación exhaustiva de los tres, en un solo token bien firmado.
func TestSuperAdminAuth_ValidSignature_StillEnforcesActiveDeletedTokenVersion(t *testing.T) {
	setSuperAdminTestConfig(t, false)
	db := setupSuperAdminAuthTestDB(t)

	cases := []struct {
		name         string
		active       bool
		softDelete   bool
		dbVersion    uint
		claimVersion uint
	}{
		{"activo + version correcta", true, false, 0, 0},
		{"inactivo", false, false, 0, 0},
		{"eliminado", true, true, 0, 0},
		{"version desactualizada", true, false, 2, 0},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := createSAUser(t, db, fmt.Sprintf("u%d@example.com", i), "admin", tc.active, tc.dbVersion)
			if tc.softDelete {
				db.Delete(&user)
			}
			token := mintSAToken(t, validClaims(user.ID, "admin", tc.claimVersion), testSAJWTSecret)
			got := statusForSAToken(t, token)
			wantOK := tc.active && !tc.softDelete && tc.dbVersion == tc.claimVersion
			if wantOK && got != fiber.StatusOK {
				t.Fatalf("%s: status = %d, want 200", tc.name, got)
			}
			if !wantOK && got != fiber.StatusUnauthorized {
				t.Fatalf("%s: status = %d, want 401 (una firma válida no debe bastar)", tc.name, got)
			}
		})
	}
}

// ==================== Compatibilidad de tokens legacy (piso de versión, solo en producción) ====================

func TestSuperAdminAuth_LegacyTokenWithoutJWTVersion_RejectedInProdOnly(t *testing.T) {
	db := setupSuperAdminAuthTestDB(t)
	user := createSAUser(t, db, "admin@example.com", "admin", true, 0)

	claims := validClaims(user.ID, "admin", 0)
	claims.SAJWTVersion = 0 // simula un token emitido antes de la Fase 4

	t.Run("desarrollo: se permite (compatibilidad)", func(t *testing.T) {
		setSuperAdminTestConfig(t, false)
		token := mintSAToken(t, claims, testSAJWTSecret)
		if got := statusForSAToken(t, token); got != fiber.StatusOK {
			t.Fatalf("status = %d, want 200 en desarrollo", got)
		}
	})

	t.Run("producción: se rechaza (forzar relogin)", func(t *testing.T) {
		setSuperAdminTestConfig(t, true)
		token := mintSAToken(t, claims, testSAJWTSecret)
		if got := statusForSAToken(t, token); got != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 en producción (token legacy sin sa_jwt_version)", got)
		}
	})
}
