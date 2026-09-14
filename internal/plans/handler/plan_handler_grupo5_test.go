package handler

// Fase 5 (etapa 3, Grupo 5, Parte A — planes): tests de NEGOCIO (no solo wiring/autorización, ya
// cubierto en internal/superadmin/route_wiring_test.go y pkg/middleware/sa_permissions_planes_test.go).

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

func setupPlanHandlerGrupo5DB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_journal_mode=WAL&_busy_timeout=15000", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.SaasPlan{}, &database.SaasPlanModule{}, &database.SaasPlanCycle{},
		&database.SaasSubscription{}, &database.AuditLog{},
	); err != nil {
		t.Fatal(err)
	}
	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })
	return db
}

func injectPlanActor(userID uint) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Locals("sa_user_id", userID)
		return c.Next()
	}
}

// POST /plans: camino feliz — crea el plan y escribe el AuditLog nuevo.
func TestPlanCreateAPI_WritesAuditLog(t *testing.T) {
	db := setupPlanHandlerGrupo5DB(t)

	h := NewPlanHandler()
	app := fiber.New()
	app.Post("/api/superadmin/plans", injectPlanActor(5), h.CreateAPI)

	req := httptest.NewRequest("POST", "/api/superadmin/plans", strings.NewReader(`{"name":"Pro Plus","price":149}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var log database.AuditLog
	if err := db.Where("action = ?", "plan_created").First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
	if log.UserID != 5 {
		t.Errorf("UserID = %d, want 5", log.UserID)
	}
	if !strings.Contains(log.Payload, "Pro Plus") {
		t.Errorf("el payload no registró el nombre del plan: %s", log.Payload)
	}

	var count int64
	db.Model(&database.SaasPlan{}).Where("name = ?", "Pro Plus").Count(&count)
	if count != 1 {
		t.Fatalf("el plan no se creó realmente: %d filas", count)
	}
}

// DELETE /plans/:id: RBAC concede la acción (planes.destroy resuelto en el middleware), pero la
// validación de negocio existente ("no se puede eliminar: el plan tiene suscripciones asociadas")
// sigue rechazando — RBAC no la reemplazó. Ese camino de error no debe dejar AuditLog.
func TestPlanDeleteAPI_BusinessValidationStillAppliesRegardlessOfRBAC(t *testing.T) {
	db := setupPlanHandlerGrupo5DB(t)
	plan := database.SaasPlan{Name: "Pro", Price: 99, Active: true}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	sub := database.SaasSubscription{TenantID: 1, PlanID: plan.ID, Status: database.SaasSubActive}
	if err := db.Create(&sub).Error; err != nil {
		t.Fatal(err)
	}

	h := NewPlanHandler()
	app := fiber.New()
	app.Delete("/api/superadmin/plans/:id", injectPlanActor(5), h.DeleteAPI)

	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/superadmin/plans/%d", plan.ID), nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode == fiber.StatusOK {
		t.Fatalf("status = 200 inesperado — un plan con suscripciones asociadas no debería poder eliminarse")
	}

	var count int64
	db.Model(&database.AuditLog{}).Count(&count)
	if count != 0 {
		t.Fatalf("se escribió un AuditLog aunque la validación de negocio rechazó la operación: %d filas", count)
	}

	var stillThere database.SaasPlan
	if err := db.First(&stillThere, plan.ID).Error; err != nil {
		t.Fatalf("el plan no debería haber sido eliminado: %v", err)
	}
}
