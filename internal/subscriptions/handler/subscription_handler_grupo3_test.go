package handler

// Fase 5 (etapa 3, Grupo 3 — suscripciones): test de NEGOCIO (no solo wiring/autorización, ya
// cubierto en internal/superadmin/route_wiring_test.go y
// pkg/middleware/sa_permissions_suscripciones_test.go) para la auditoría agregada en esta etapa.

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

func init() {
	// Ver comentario equivalente en internal/superadmin/route_wiring_test.go.
	logger.Init(&config.Config{LogLevel: "error", AppEnv: "development"})
}

func setupSubscriptionHandlerGrupo3DB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_journal_mode=WAL&_busy_timeout=15000", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.Tenant{}, &database.SaasSubscription{}, &database.SaasSubscriptionEvent{}, &database.AuditLog{},
	); err != nil {
		t.Fatal(err)
	}
	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })

	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{AppEnv: "development"}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	return db
}

func injectSubscriptionActor(userID uint) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Locals("sa_user_id", userID)
		return c.Next()
	}
}

// PATCH /subscriptions/:id/suspend: camino feliz — escribe el AuditLog con from/to correctos, sin
// filtrar nada más allá de lo necesario, y de verdad suspende tenant + suscripción (RBAC no
// reemplazó la lógica de negocio).
func TestSuspendAPI_WritesAuditLogAndSuspendsTenant(t *testing.T) {
	db := setupSubscriptionHandlerGrupo3DB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "saas_tenant_acme", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	sub := database.SaasSubscription{TenantID: tenant.ID, PlanID: 1, Status: database.SaasSubActive}
	if err := db.Create(&sub).Error; err != nil {
		t.Fatal(err)
	}

	h := NewSubscriptionHandler()
	app := fiber.New()
	app.Patch("/api/superadmin/subscriptions/:id/suspend", injectSubscriptionActor(9), h.SuspendAPI)

	req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/superadmin/subscriptions/%d/suspend", sub.ID),
		strings.NewReader(`{"reason":"impago"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var log database.AuditLog
	if err := db.Where("action = ?", "subscription_suspended").First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
	if log.UserID != 9 {
		t.Errorf("UserID = %d, want 9", log.UserID)
	}
	if log.TenantID != tenant.ID {
		t.Errorf("TenantID = %d, want %d", log.TenantID, tenant.ID)
	}
	if !strings.Contains(log.Payload, `"from":"active"`) || !strings.Contains(log.Payload, `"to":"suspended"`) {
		t.Errorf("el payload no registró el cambio de estado correctamente: %s", log.Payload)
	}

	var reloadedSub database.SaasSubscription
	db.First(&reloadedSub, sub.ID)
	if reloadedSub.Status != database.SaasSubSuspended {
		t.Errorf("la suscripción no quedó suspendida: %q", reloadedSub.Status)
	}
	var reloadedTenant database.Tenant
	db.First(&reloadedTenant, tenant.ID)
	if reloadedTenant.Status != "suspended" {
		t.Errorf("el tenant no quedó suspendido: %q", reloadedTenant.Status)
	}
}

// PATCH /subscriptions/:id/cancel sin motivo: RBAC concede la acción (suscripciones.change_status
// resuelto en el middleware), pero la validación de negocio existente ("el motivo es obligatorio")
// sigue rechazando — confirma que el RBAC nuevo no reemplazó ninguna validación existente, y que
// en ese camino de error no se escribe ningún AuditLog espurio.
func TestCancelAPI_BusinessValidationStillAppliesRegardlessOfRBAC(t *testing.T) {
	db := setupSubscriptionHandlerGrupo3DB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "saas_tenant_acme", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	sub := database.SaasSubscription{TenantID: tenant.ID, PlanID: 1, Status: database.SaasSubActive}
	if err := db.Create(&sub).Error; err != nil {
		t.Fatal(err)
	}

	h := NewSubscriptionHandler()
	app := fiber.New()
	app.Patch("/api/superadmin/subscriptions/:id/cancel", injectSubscriptionActor(9), h.CancelAPI)

	req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/superadmin/subscriptions/%d/cancel", sub.ID),
		strings.NewReader(`{"reason":""}`)) // motivo vacío — Cancel() lo exige
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode == fiber.StatusOK {
		t.Fatalf("status = 200 inesperado — el motivo es obligatorio para cancelar")
	}

	var count int64
	db.Model(&database.AuditLog{}).Count(&count)
	if count != 0 {
		t.Fatalf("se escribió un AuditLog aunque la validación de negocio rechazó la operación: %d filas", count)
	}

	var reloaded database.SaasSubscription
	db.First(&reloaded, sub.ID)
	if reloaded.Status != database.SaasSubActive {
		t.Errorf("la suscripción cambió de estado inesperadamente: %q", reloaded.Status)
	}
}
