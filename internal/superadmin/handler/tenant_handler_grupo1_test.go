package handler

// Fase 5 (etapa 3, Grupo 1 — empresas/tenants): tests de NEGOCIO (no solo wiring/autorización,
// ya cubierto en internal/superadmin/route_wiring_test.go) para la auditoría que se agregó en
// esta etapa — confirma que AuditLog se escribe con los datos correctos y, especialmente, que
// nunca contiene secretos.

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

func setupTenantHandlerGrupo1DB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.Tenant{}, &database.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })
	return db
}

// injectActor simula lo que dejaría SuperAdminAuthAPI + RequireSAPermission ya resueltos — este
// test se enfoca en la lógica de auditoría del handler, no en la autorización (esa ya está
// probada con la ruta real en route_wiring_test.go).
func injectActor(userID uint) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Locals("sa_user_id", userID)
		return c.Next()
	}
}

// PATCH /tenants/:id/status escribe un AuditLog con el estado anterior y el nuevo.
func TestToggleStatusAPI_WritesAuditLogWithOldAndNewStatus(t *testing.T) {
	db := setupTenantHandlerGrupo1DB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "saas_tenant_acme", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}

	h := NewTenantHandler()
	app := fiber.New()
	app.Patch("/api/superadmin/tenants/:id/status", injectActor(42), h.ToggleStatusAPI)

	req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/superadmin/tenants/%d/status", tenant.ID),
		strings.NewReader(`{"status":"suspended"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var log database.AuditLog
	if err := db.Where("action = ?", "tenant_status_changed").First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
	if log.UserID != 42 {
		t.Errorf("UserID = %d, want 42", log.UserID)
	}
	if log.TenantID != tenant.ID {
		t.Errorf("TenantID = %d, want %d", log.TenantID, tenant.ID)
	}
	if !strings.Contains(log.Payload, `"from":"active"`) || !strings.Contains(log.Payload, `"to":"suspended"`) {
		t.Errorf("el payload no registró el cambio de estado correctamente: %s", log.Payload)
	}

	var reloaded database.Tenant
	db.First(&reloaded, tenant.ID)
	if reloaded.Status != "suspended" {
		t.Errorf("el estado del tenant no se actualizó: %q", reloaded.Status)
	}
}

// DestroyCompleteAPI: sin operations_key configurada, la request se rechaza ANTES de tocar el
// tenant — y, sobre todo, NO debe escribir ningún AuditLog con el operations_key recibido en el
// body (ni con nada del body en absoluto).
func TestDestroyCompleteAPI_RejectsWithoutOperationsKey_NoAuditLeak(t *testing.T) {
	db := setupTenantHandlerGrupo1DB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "saas_tenant_acme", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}

	h := NewTenantHandler()
	app := fiber.New()
	app.Post("/api/superadmin/tenants/:id/destroy-complete", injectActor(1), h.DestroyCompleteAPI)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/superadmin/tenants/%d/destroy-complete", tenant.ID),
		strings.NewReader(`{"operations_key":"un-secreto-cualquiera","confirm_slug":"acme"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// Sin operations-key configurada en el servidor de test, el servicio debe rechazar (400/403),
	// nunca 200.
	if resp.StatusCode == fiber.StatusOK {
		t.Fatalf("status = 200 inesperado — debería requerir operations_key configurada")
	}

	var count int64
	db.Model(&database.AuditLog{}).Count(&count)
	if count != 0 {
		t.Fatalf("se escribió un AuditLog aunque la operación fue rechazada, o contiene datos que no debería: %d filas", count)
	}

	// El tenant debe seguir existiendo intacto.
	var stillThere database.Tenant
	if err := db.First(&stillThere, tenant.ID).Error; err != nil {
		t.Fatalf("el tenant no debería haber sido tocado: %v", err)
	}
}
