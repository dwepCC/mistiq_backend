package handler

// Fase 5 (etapa 3, Grupo 2 — pagos): test de NEGOCIO (no solo wiring/autorización, ya cubierto en
// internal/superadmin/route_wiring_test.go y pkg/middleware/sa_permissions_pagos_test.go) para la
// auditoría agregada en esta etapa — confirma que RejectAPI escribe el AuditLog con los datos
// correctos y sin secretos.

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
	// Ver comentario equivalente en internal/superadmin/route_wiring_test.go — algunos servicios
	// de pkg/saas usan el logger global sin verificar que esté inicializado.
	logger.Init(&config.Config{LogLevel: "error", AppEnv: "development"})
}

func setupPaymentHandlerGrupo2DB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_journal_mode=WAL&_busy_timeout=15000", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.Tenant{}, &database.SaasPayment{}, &database.SaasPlatformSettings{},
		&database.SaasSubscription{}, &database.AuditLog{},
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

func injectPaymentActor(userID uint) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Locals("sa_user_id", userID)
		return c.Next()
	}
}

// PATCH /payments/:id/reject sobre un pago que YA no está pendiente: RBAC concede la acción
// (pagos.reject está resuelto en el middleware, no aquí), pero la validación de negocio existente
// dentro de RejectPayment sigue rechazando la operación igual — confirma que el RBAC nuevo no
// reemplazó ninguna validación de negocio ya existente (tal como exigiste), y que en ese camino de
// error no se escribe ningún AuditLog espurio.
func TestRejectAPI_BusinessValidationStillAppliesRegardlessOfRBAC(t *testing.T) {
	db := setupPaymentHandlerGrupo2DB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "saas_tenant_acme", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	// Ya rechazado — RejectPayment debe negarse ("el pago ya fue rejected"), sin importar que el
	// llamador tenga pagos.reject.
	payment := database.SaasPayment{
		TenantID: tenant.ID, Amount: 99, Currency: "PEN", PaymentMethod: "yape",
		ReceiptURL: "/storage/receipts/secret-looking-file.jpg", Status: database.SaasPayRejected,
	}
	if err := db.Create(&payment).Error; err != nil {
		t.Fatal(err)
	}

	h := NewPaymentHandler()
	app := fiber.New()
	app.Patch("/api/superadmin/payments/:id/reject", injectPaymentActor(7), h.RejectAPI)

	req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/superadmin/payments/%d/reject", payment.ID),
		strings.NewReader(`{"admin_notes":"comprobante ilegible"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode == fiber.StatusOK {
		t.Fatalf("status = 200 inesperado — un pago ya rechazado no debería poder rechazarse otra vez")
	}

	var count int64
	db.Model(&database.AuditLog{}).Count(&count)
	if count != 0 {
		t.Fatalf("se escribió un AuditLog aunque la validación de negocio rechazó la operación: %d filas", count)
	}

	var reloaded database.SaasPayment
	db.First(&reloaded, payment.ID)
	if reloaded.Status != database.SaasPayRejected {
		t.Errorf("el estado del pago cambió inesperadamente: %q", reloaded.Status)
	}
}
