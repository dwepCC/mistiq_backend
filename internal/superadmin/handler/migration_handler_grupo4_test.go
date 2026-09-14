package handler

// Fase 5 (etapa 3, Grupo 4 — migraciones, ALTO RIESGO): test de NEGOCIO (no solo wiring/
// autorización, ya cubierto en internal/superadmin/route_wiring_test.go y
// pkg/middleware/sa_permissions_migraciones_test.go) para el camino feliz de PauseAPI/ResumeAPI —
// confirma que el AuditLog ya existente (logMigrationAudit) sigue funcionando, y que RBAC no
// reemplazó ninguna validación de negocio.

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

func setupMigrationHandlerGrupo4DB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_journal_mode=WAL&_busy_timeout=15000", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.Tenant{}, &database.TenantSchemaVersion{}, &database.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })
	return db
}

func injectMigrationActor(userID uint) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Locals("sa_user_id", userID)
		return c.Next()
	}
}

// PATCH .../pause: camino feliz — pausa el tenant y escribe el AuditLog ya existente
// (logMigrationAudit, "migration.pause"), reutilizado sin cambios por esta fase.
func TestPauseAPI_PausesTenantAndWritesExistingAuditLog(t *testing.T) {
	db := setupMigrationHandlerGrupo4DB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "saas_tenant_acme", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	tsv := database.TenantSchemaVersion{TenantID: tenant.ID, CurrentVersion: 30, TargetVersion: 30, Status: database.TenantSchemaStatusPending}
	if err := db.Create(&tsv).Error; err != nil {
		t.Fatal(err)
	}

	h := NewMigrationHandler()
	app := fiber.New()
	app.Post("/api/superadmin/migrations/:tenantId/pause", injectMigrationActor(11), h.PauseAPI)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/superadmin/migrations/%d/pause", tenant.ID), nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var reloaded database.TenantSchemaVersion
	db.First(&reloaded, "tenant_id = ?", tenant.ID)
	if reloaded.Status != database.TenantSchemaStatusPaused {
		t.Errorf("el tenant no quedó pausado: %q", reloaded.Status)
	}

	var log database.AuditLog
	if err := db.Where("action = ?", "migration.pause").First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
	if log.UserID != 11 {
		t.Errorf("UserID = %d, want 11", log.UserID)
	}
	if log.TenantID != tenant.ID {
		t.Errorf("TenantID = %d, want %d", log.TenantID, tenant.ID)
	}
}

// MigrateAPI sobre un tenant pausado: RBAC concede la acción (migraciones.run resuelto en el
// middleware), pero la validación de negocio existente ("tenant en pausa; use resume primero",
// dentro de runIncrementalForTenant) sigue rechazando — RBAC no reemplazó ninguna validación
// existente. Ese camino de error no debe dejar un AuditLog de éxito.
//
// (Se usa MigrateAPI y no RetryAPI para esta prueba: Retry primero RESETEA el estado a "pending"
// antes de migrar — comportamiento de negocio ya existente, no tocado por esta fase — así que el
// check de "en pausa" nunca lo alcanza a bloquear. Migrate no resetea nada, así que sí es el
// camino correcto para demostrar que la validación de pausa sigue vigente.)
func TestMigrateAPI_BusinessValidationStillAppliesRegardlessOfRBAC(t *testing.T) {
	db := setupMigrationHandlerGrupo4DB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "saas_tenant_acme", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	tsv := database.TenantSchemaVersion{TenantID: tenant.ID, CurrentVersion: 30, TargetVersion: 31, Status: database.TenantSchemaStatusPaused}
	if err := db.Create(&tsv).Error; err != nil {
		t.Fatal(err)
	}

	h := NewMigrationHandler()
	app := fiber.New()
	app.Post("/api/superadmin/migrations/:tenantId/migrate", injectMigrationActor(11), h.MigrateAPI)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/superadmin/migrations/%d/migrate", tenant.ID), nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode == fiber.StatusOK {
		t.Fatalf("status = 200 inesperado — un tenant en pausa no debería poder migrar sin resume primero")
	}

	var count int64
	db.Model(&database.AuditLog{}).Count(&count)
	if count != 0 {
		t.Fatalf("se escribió un AuditLog aunque la validación de negocio rechazó la operación: %d filas", count)
	}

	var reloaded database.TenantSchemaVersion
	db.First(&reloaded, "tenant_id = ?", tenant.ID)
	if reloaded.Status != database.TenantSchemaStatusPaused {
		t.Errorf("el estado cambió inesperadamente: %q", reloaded.Status)
	}
}
