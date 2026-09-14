package database

// Fase 6 (pre-migración de usuarios reales, Grupo 7): tests de ensureBootstrapSuperadmin —
// extraída de SeedCentral para poder probar aisladamente la condición que decide si hace falta
// crear el superadmin de arranque. El bug encontrado: la condición anterior contaba CUALQUIER
// SuperAdminUser (Count sin Where), así que un solo "admin" ya bastaba para no crear el
// superadmin de emergencia. La condición corregida exige Role=="superadmin" && Active==true.

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupBootstrapSuperadminTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "sa_bootstrap_test.db")
	dsn := "file:" + dbPath + "?_journal_mode=WAL&_busy_timeout=15000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(&SuperAdminUser{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func countSuperAdminUsers(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&SuperAdminUser{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

// createBootstrapTestUser inserta un SuperAdminUser de fixture, forzando Active con un UPDATE
// aparte cuando corresponde false — Active tiene `gorm:"default:true"`, así que Create() omite
// el valor explícito false (zero-value de bool) y la BD aplicaría su propio default en su lugar
// (mismo pitfall documentado en Fase 1 / Grupo 7).
func createBootstrapTestUser(t *testing.T, db *gorm.DB, email, role string, active bool) SuperAdminUser {
	t.Helper()
	u := SuperAdminUser{Name: "T", Email: email, Role: role, Active: true}
	if err := u.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	if !active {
		if err := db.Model(&u).UpdateColumn("active", false).Error; err != nil {
			t.Fatal(err)
		}
		u.Active = false
	}
	return u
}

// 1. 0 usuarios → debe crear el superadmin de bootstrap.
func TestEnsureBootstrapSuperadmin_NoUsers_CreatesBootstrap(t *testing.T) {
	db := setupBootstrapSuperadminTestDB(t)

	if err := ensureBootstrapSuperadmin(db); err != nil {
		t.Fatalf("ensureBootstrapSuperadmin: %v", err)
	}
	if got := countSuperAdminUsers(t, db); got != 1 {
		t.Fatalf("count = %d, want 1 (se creó el superadmin de bootstrap)", got)
	}
	var created SuperAdminUser
	if err := db.Where("email = ?", "superadmin@saas.com").First(&created).Error; err != nil {
		t.Fatalf("no se encontró el superadmin de bootstrap: %v", err)
	}
	if created.Role != "superadmin" {
		t.Fatalf("Role = %q, want superadmin", created.Role)
	}
}

// 2. Solo un "admin" (sin ningún superadmin) → el bug original: NO debía considerarse que ya
// existe un superadmin operativo. La condición corregida SÍ debe crear el bootstrap.
func TestEnsureBootstrapSuperadmin_OnlyAdminExists_StillConsidersNoOperationalSuperadmin(t *testing.T) {
	db := setupBootstrapSuperadminTestDB(t)
	createBootstrapTestUser(t, db, "admin@example.com", "admin", true)

	if err := ensureBootstrapSuperadmin(db); err != nil {
		t.Fatalf("ensureBootstrapSuperadmin: %v", err)
	}
	if got := countSuperAdminUsers(t, db); got != 2 {
		t.Fatalf("count = %d, want 2 (el admin existente + el superadmin de bootstrap creado)", got)
	}
	var superadminCount int64
	db.Model(&SuperAdminUser{}).Where("role = ? AND active = ?", "superadmin", true).Count(&superadminCount)
	if superadminCount != 1 {
		t.Fatalf("superadmins operativos = %d, want 1 — el bug original NO creaba ninguno en este escenario", superadminCount)
	}
}

// 3. Ya existe 1 superadmin activo → NO debe crear otro automáticamente.
func TestEnsureBootstrapSuperadmin_OneActiveSuperadmin_DoesNotCreateAnother(t *testing.T) {
	db := setupBootstrapSuperadminTestDB(t)
	createBootstrapTestUser(t, db, "real@example.com", "superadmin", true)

	if err := ensureBootstrapSuperadmin(db); err != nil {
		t.Fatalf("ensureBootstrapSuperadmin: %v", err)
	}
	if got := countSuperAdminUsers(t, db); got != 1 {
		t.Fatalf("count = %d, want 1 (no debió crear un segundo superadmin)", got)
	}
}

// 4. Superadmin inactivo (sin ningún otro superadmin activo) → NO debe asumirse silenciosamente
// que ya existe uno operativo. La condición corregida excluye Active=false, así que SÍ intenta
// crear el bootstrap — y, como el email fijo "superadmin@saas.com" no coincide con el del
// inactivo, la creación tiene éxito (caso feliz). El caso "el inactivo YA tiene ese email exacto"
// es un edge case documentado en el comentario de ensureBootstrapSuperadmin — no se resuelve
// aquí (requeriría una decisión de negocio: reactivar vs. fallar vs. otro email), pero este test
// confirma que, como mínimo, NUNCA se lo trata como "ya hay uno operativo".
func TestEnsureBootstrapSuperadmin_InactiveSuperadmin_DoesNotCountAsOperational(t *testing.T) {
	db := setupBootstrapSuperadminTestDB(t)
	createBootstrapTestUser(t, db, "inactive@example.com", "superadmin", false)

	if err := ensureBootstrapSuperadmin(db); err != nil {
		t.Fatalf("ensureBootstrapSuperadmin: %v", err)
	}
	var operationalCount int64
	db.Model(&SuperAdminUser{}).Where("role = ? AND active = ?", "superadmin", true).Count(&operationalCount)
	if operationalCount != 1 {
		t.Fatalf("superadmins operativos = %d, want 1 (el bootstrap debió crear uno, ya que el inactivo no cuenta)", operationalCount)
	}
	if got := countSuperAdminUsers(t, db); got != 2 {
		t.Fatalf("count total = %d, want 2 (el inactivo original + el bootstrap nuevo)", got)
	}
}

// Un admin inactivo tampoco cuenta como superadmin operativo, obviamente — pero se prueba
// explícito para no dejar ninguna combinación sin cubrir.
func TestEnsureBootstrapSuperadmin_InactiveAdmin_StillCreatesBootstrap(t *testing.T) {
	db := setupBootstrapSuperadminTestDB(t)
	createBootstrapTestUser(t, db, "admin@example.com", "admin", false)

	if err := ensureBootstrapSuperadmin(db); err != nil {
		t.Fatalf("ensureBootstrapSuperadmin: %v", err)
	}
	var operationalCount int64
	db.Model(&SuperAdminUser{}).Where("role = ? AND active = ?", "superadmin", true).Count(&operationalCount)
	if operationalCount != 1 {
		t.Fatalf("superadmins operativos = %d, want 1", operationalCount)
	}
}

// Múltiples superadmins activos → tampoco crea uno adicional (no solo el caso "exactamente 1").
func TestEnsureBootstrapSuperadmin_MultipleActiveSuperadmins_DoesNotCreateAnother(t *testing.T) {
	db := setupBootstrapSuperadminTestDB(t)
	createBootstrapTestUser(t, db, "sa1@example.com", "superadmin", true)
	createBootstrapTestUser(t, db, "sa2@example.com", "superadmin", true)

	if err := ensureBootstrapSuperadmin(db); err != nil {
		t.Fatalf("ensureBootstrapSuperadmin: %v", err)
	}
	if got := countSuperAdminUsers(t, db); got != 2 {
		t.Fatalf("count = %d, want 2 (sin cambios)", got)
	}
}
