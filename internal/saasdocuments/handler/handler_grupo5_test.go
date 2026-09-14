package handler

// Fase 5 (etapa 3, Grupo 5, Parte B — documentos): tests de NEGOCIO (no solo wiring/autorización,
// ya cubierto en internal/superadmin/route_wiring_test.go y
// pkg/middleware/sa_permissions_documentos_test.go) para la auditoría agregada y la corrección
// del bug de actor (superadmin_id → sa_user_id).

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

func setupDocumentsHandlerGrupo5DB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_journal_mode=WAL&_busy_timeout=15000", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.SaasDocumentPackage{}, &database.SaasTenantDocumentPackage{}, &database.Tenant{}, &database.AuditLog{},
	); err != nil {
		t.Fatal(err)
	}
	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })
	return db
}

func injectDocumentsActor(userID uint) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Locals("sa_user_id", userID)
		return c.Next()
	}
}

// PATCH .../approve: camino feliz — acredita los documentos, escribe el AuditLog nuevo con el
// actor REAL (confirma el arreglo del bug superadmin_id → sa_user_id).
func TestDocumentApproveAPI_CreditsDocumentsAndWritesAuditLogWithRealActor(t *testing.T) {
	db := setupDocumentsHandlerGrupo5DB(t)
	pkg := database.SaasDocumentPackage{Name: "50 documentos", DocumentsQty: 50, Price: 20, IsActive: true}
	if err := db.Create(&pkg).Error; err != nil {
		t.Fatal(err)
	}
	tp := database.SaasTenantDocumentPackage{
		TenantID: 1, PackageID: pkg.ID, DocumentsQty: 50, Status: database.SaasDocPkgPendingReview,
	}
	if err := db.Create(&tp).Error; err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	app := fiber.New()
	app.Patch("/api/superadmin/document-packages/purchases/:id/approve", injectDocumentsActor(13), h.ApproveAPI)

	req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/superadmin/document-packages/purchases/%d/approve", tp.ID),
		strings.NewReader(`{"notes":"comprobante verificado"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var reloaded database.SaasTenantDocumentPackage
	db.First(&reloaded, tp.ID)
	if reloaded.Status != database.SaasDocPkgApproved {
		t.Errorf("status = %q, want approved", reloaded.Status)
	}
	if reloaded.RemainingDocuments != 50 {
		t.Errorf("RemainingDocuments = %d, want 50 (no se acreditaron los documentos comprados)", reloaded.RemainingDocuments)
	}
	if reloaded.ApprovedBy == nil || *reloaded.ApprovedBy != 13 {
		t.Errorf("ApprovedBy = %v, want 13 — el bug superadmin_id/sa_user_id no quedó corregido", reloaded.ApprovedBy)
	}

	var log database.AuditLog
	if err := db.Where("action = ?", "document_package_purchase_approved").First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
	if log.UserID != 13 {
		t.Errorf("AuditLog.UserID = %d, want 13", log.UserID)
	}
}

// PATCH .../approve sobre una solicitud ya revisada: RBAC concede la acción
// (documentos.approve_purchase resuelto en el middleware), pero la validación de negocio
// existente (ErrPackageAlreadyReviewed) sigue rechazando — sin dejar AuditLog.
func TestDocumentApproveAPI_BusinessValidationStillAppliesRegardlessOfRBAC(t *testing.T) {
	db := setupDocumentsHandlerGrupo5DB(t)
	pkg := database.SaasDocumentPackage{Name: "50 documentos", DocumentsQty: 50, Price: 20, IsActive: true}
	if err := db.Create(&pkg).Error; err != nil {
		t.Fatal(err)
	}
	tp := database.SaasTenantDocumentPackage{
		TenantID: 1, PackageID: pkg.ID, DocumentsQty: 50, Status: database.SaasDocPkgApproved, RemainingDocuments: 50,
	}
	if err := db.Create(&tp).Error; err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	app := fiber.New()
	app.Patch("/api/superadmin/document-packages/purchases/:id/approve", injectDocumentsActor(13), h.ApproveAPI)

	req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/superadmin/document-packages/purchases/%d/approve", tp.ID), nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode == fiber.StatusOK {
		t.Fatalf("status = 200 inesperado — una solicitud ya aprobada no debería poder re-aprobarse")
	}

	var count int64
	db.Model(&database.AuditLog{}).Count(&count)
	if count != 0 {
		t.Fatalf("se escribió un AuditLog aunque la validación de negocio rechazó la operación: %d filas", count)
	}
}
