package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"tukifac/internal/superadmin/service"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// setupSARoleHandlerTestDB sigue el mismo patrón que el resto del paquete (sqlite en memoria,
// cache compartido por nombre de test — ver setupCalcTotalsDB en cashbank_handler_test.go).
func setupSARoleHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.SuperAdminUser{}, &database.SARole{}, &database.SAPermission{}, &database.SARolePermission{},
		&database.TenantRole{}, &database.TenantPermission{}, &database.TenantRolePermission{},
		&database.AuditLog{},
	); err != nil {
		t.Fatal(err)
	}
	// logSARoleAudit (Grupo 7) escribe en database.CentralDB — se apunta al fixture de este test
	// (con AuditLog ya migrado arriba) para que la auditoría sea real y verificable, no ruido.
	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })
	return db
}

// newSARoleTestApp monta los handlers directamente (sin SuperAdminAuthAPI, que ya tiene su
// propia cobertura de tests) — igual que master_access_test.go prueba el guard aislado del
// parseo del JWT. Autorización (RequireSAPermission) ya no vive aquí (se agregó en routes.go,
// Grupo 7) — este archivo prueba la lógica de negocio de cada handler asumiendo que la
// autorización de ruta ya pasó. El techo de delegación (CanDelegateAll), en cambio, SÍ vive
// dentro de DeleteAPI/SetRolePermissionsAPI y depende de sa_claims — por eso este helper inyecta
// un actor superadmin real por defecto (bypass total de CanDelegateAll), para que estos tests de
// CRUD sigan probando exactamente lo que probaban antes de este grupo. Los tests DEL techo de
// delegación en sí usan newSARoleTestAppWithClaims con un actor no-superadmin explícito.
func newSARoleTestApp(db *gorm.DB) *fiber.App {
	return newSARoleTestAppWithClaims(db, &middleware.SuperAdminClaims{UserID: 1, Role: "superadmin"})
}

func newSARoleTestAppWithClaims(db *gorm.DB, claims *middleware.SuperAdminClaims) *fiber.App {
	h := &SARoleHandler{svc: service.NewSARoleService(db)}
	app := fiber.New()
	inject := func(c fiber.Ctx) error {
		c.Locals("sa_claims", claims)
		c.Locals("sa_user_id", claims.UserID)
		return c.Next()
	}
	app.Get("/api/superadmin/roles", inject, h.ListAPI)
	app.Get("/api/superadmin/roles/:id", inject, h.GetAPI)
	app.Post("/api/superadmin/roles", inject, h.CreateAPI)
	app.Put("/api/superadmin/roles/:id", inject, h.UpdateAPI)
	app.Delete("/api/superadmin/roles/:id", inject, h.DeleteAPI)
	app.Get("/api/superadmin/roles/:id/permissions", inject, h.RolePermissionsAPI)
	app.Put("/api/superadmin/roles/:id/permissions", inject, h.SetRolePermissionsAPI)
	app.Get("/api/superadmin/permissions", inject, h.PermissionsCatalogAPI)
	return app
}

func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func doRequest(t *testing.T, app *fiber.App, req *http.Request) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp, out
}

func seedRoleHandlerPermission(t *testing.T, db *gorm.DB, module, action string) database.SAPermission {
	t.Helper()
	p := database.SAPermission{Module: module, Action: action, Label: module + "." + action}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// 1. GET roles.
func TestSARoleHandler_ListAPI(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	db.Create(&database.SARole{Name: "Soporte"})
	db.Create(&database.SARole{Name: "Finanzas"})

	resp, out := doRequest(t, app, jsonRequest(t, "GET", "/api/superadmin/roles", nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	data, ok := out["data"].([]any)
	if !ok || len(data) != 2 {
		t.Fatalf("data = %#v, esperado 2 roles", out["data"])
	}
}

// 2. GET rol por ID.
func TestSARoleHandler_GetAPI(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	role := database.SARole{Name: "Soporte"}
	db.Create(&role)

	resp, out := doRequest(t, app, jsonRequest(t, "GET", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	data := out["data"].(map[string]any)
	if data["name"] != "Soporte" {
		t.Fatalf("data = %#v", data)
	}

	resp404, _ := doRequest(t, app, jsonRequest(t, "GET", "/api/superadmin/roles/99999", nil))
	if resp404.StatusCode != fiber.StatusNotFound {
		t.Fatalf("status rol inexistente = %d, want 404", resp404.StatusCode)
	}
}

// 3. POST crear rol.
func TestSARoleHandler_CreateAPI(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	resp, out := doRequest(t, app, jsonRequest(t, "POST", "/api/superadmin/roles", map[string]any{
		"name": "Auditor", "description": "Solo lectura",
	}))
	if resp.StatusCode != fiber.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%v", resp.StatusCode, out)
	}
	if out["success"] != true {
		t.Fatalf("success != true: %#v", out)
	}
	data := out["data"].(map[string]any)
	if data["name"] != "Auditor" || data["is_system"] != false {
		t.Fatalf("rol creado incorrecto: %#v", data)
	}
}

// 4. POST con nombre inválido.
func TestSARoleHandler_CreateAPI_InvalidName(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	resp, _ := doRequest(t, app, jsonRequest(t, "POST", "/api/superadmin/roles", map[string]any{
		"name": "   ",
	}))
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// 5. POST con nombre duplicado.
func TestSARoleHandler_CreateAPI_DuplicateName(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	doRequest(t, app, jsonRequest(t, "POST", "/api/superadmin/roles", map[string]any{"name": "Auditor"}))
	resp, _ := doRequest(t, app, jsonRequest(t, "POST", "/api/superadmin/roles", map[string]any{"name": "Auditor"}))
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("status = %d, want 409 (conflicto por nombre duplicado)", resp.StatusCode)
	}
}

// 6. PUT actualizar rol.
func TestSARoleHandler_UpdateAPI(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	role := database.SARole{Name: "Auditor"}
	db.Create(&role)

	resp, out := doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), map[string]any{
		"name": "Auditor Senior", "description": "actualizado",
	}))
	if resp.StatusCode != fiber.StatusOK || out["success"] != true {
		t.Fatalf("status=%d body=%v", resp.StatusCode, out)
	}

	var reloaded database.SARole
	db.First(&reloaded, role.ID)
	if reloaded.Name != "Auditor Senior" || reloaded.Description != "actualizado" {
		t.Fatalf("no se aplicó la actualización: %+v", reloaded)
	}
}

// 7. DELETE rol personalizado.
func TestSARoleHandler_DeleteAPI_CustomRole(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	role := database.SARole{Name: "Temporal"}
	db.Create(&role)

	resp, _ := doRequest(t, app, jsonRequest(t, "DELETE", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var count int64
	db.Model(&database.SARole{}).Where("id = ?", role.ID).Count(&count)
	if count != 0 {
		t.Fatal("el rol debería haber sido eliminado")
	}
}

// 8. DELETE rol de sistema → rechazado.
func TestSARoleHandler_DeleteAPI_SystemRoleRejected(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	role := database.SARole{Name: "Admin", IsSystem: true}
	db.Create(&role)

	resp, _ := doRequest(t, app, jsonRequest(t, "DELETE", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), nil))
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("status = %d, want 409 (rol de sistema)", resp.StatusCode)
	}

	var count int64
	db.Model(&database.SARole{}).Where("id = ?", role.ID).Count(&count)
	if count != 1 {
		t.Fatal("el rol de sistema no debería haber sido eliminado")
	}
}

// 9. GET catálogo de permisos.
func TestSARoleHandler_PermissionsCatalogAPI(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	seedRoleHandlerPermission(t, db, "empresas", "view")
	seedRoleHandlerPermission(t, db, "fiscal", "cancel")

	resp, out := doRequest(t, app, jsonRequest(t, "GET", "/api/superadmin/permissions", nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	data := out["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("catálogo = %d permisos, esperado 2", len(data))
	}
}

// 10. GET permisos de rol.
func TestSARoleHandler_RolePermissionsAPI(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	role := database.SARole{Name: "Soporte"}
	db.Create(&role)
	p1 := seedRoleHandlerPermission(t, db, "empresas", "view")
	p2 := seedRoleHandlerPermission(t, db, "fiscal", "view")
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: p1.ID})
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: p2.ID})

	resp, out := doRequest(t, app, jsonRequest(t, "GET", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	data := out["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("permisos del rol = %d, esperado 2", len(data))
	}

	resp404, _ := doRequest(t, app, jsonRequest(t, "GET", "/api/superadmin/roles/99999/permissions", nil))
	if resp404.StatusCode != fiber.StatusNotFound {
		t.Fatalf("status rol inexistente = %d, want 404", resp404.StatusCode)
	}
}

// 11. PUT reemplazar permisos.
func TestSARoleHandler_SetRolePermissionsAPI_Replace(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	role := database.SARole{Name: "Soporte"}
	db.Create(&role)
	p1 := seedRoleHandlerPermission(t, db, "empresas", "view")
	p2 := seedRoleHandlerPermission(t, db, "fiscal", "view")
	p3 := seedRoleHandlerPermission(t, db, "migraciones", "view")

	resp1, _ := doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), map[string]any{
		"permission_ids": []uint{p1.ID, p2.ID},
	}))
	if resp1.StatusCode != fiber.StatusOK {
		t.Fatalf("primer PUT status = %d, want 200", resp1.StatusCode)
	}

	resp2, _ := doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), map[string]any{
		"permission_ids": []uint{p1.ID, p3.ID},
	}))
	if resp2.StatusCode != fiber.StatusOK {
		t.Fatalf("segundo PUT status = %d, want 200", resp2.StatusCode)
	}

	_, out := doRequest(t, app, jsonRequest(t, "GET", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), nil))
	data := out["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("tras el reemplazo hay %d permisos, esperado 2 (p1, p3)", len(data))
	}
	var count int64
	db.Model(&database.SARolePermission{}).Where("role_id = ? AND permission_id = ?", role.ID, p2.ID).Count(&count)
	if count != 0 {
		t.Fatal("p2 debería haber sido removido en el reemplazo")
	}
}

// 12. PUT con permiso inexistente → rechazado.
// 13. Verificar que la operación inválida no deja cambios parciales.
func TestSARoleHandler_SetRolePermissionsAPI_NonExistentPermissionRejectedNoPartialState(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	role := database.SARole{Name: "Soporte"}
	db.Create(&role)
	p1 := seedRoleHandlerPermission(t, db, "empresas", "view")
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: p1.ID})

	resp, _ := doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), map[string]any{
		"permission_ids": []uint{p1.ID, 999999},
	}))
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (permiso inexistente)", resp.StatusCode)
	}

	// El estado debe seguir siendo exactamente el de antes de la llamada rechazada.
	var count int64
	db.Model(&database.SARolePermission{}).Where("role_id = ?", role.ID).Count(&count)
	if count != 1 {
		t.Fatalf("quedaron %d relaciones tras una llamada rechazada, esperado 1 (sin cambios)", count)
	}
}

// 14. Verificar que no se puede crear/renombrar "Superadmin".
func TestSARoleHandler_RejectsSuperadminName(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	respCreate, _ := doRequest(t, app, jsonRequest(t, "POST", "/api/superadmin/roles", map[string]any{"name": "SuperAdmin"}))
	if respCreate.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("crear 'SuperAdmin': status = %d, want 400", respCreate.StatusCode)
	}

	role := database.SARole{Name: "Casi Admin"}
	db.Create(&role)
	respRename, _ := doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), map[string]any{
		"name": "superadmin",
	}))
	if respRename.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("renombrar a 'superadmin': status = %d, want 400", respRename.StatusCode)
	}

	var count int64
	db.Model(&database.SARole{}).Where("LOWER(name) = ?", "superadmin").Count(&count)
	if count != 0 {
		t.Fatal("no debe existir ningún rol llamado 'superadmin' en ninguna variación")
	}
}

// 15. Verificar que la API no modifica usuarios centrales.
func TestSARoleHandler_DoesNotModifySuperAdminUsers(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	admin := database.SuperAdminUser{Name: "Real Admin", Email: "real@example.com", Role: "superadmin"}
	if err := admin.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	db.Create(&admin)

	role := database.SARole{Name: "Auditor"}
	db.Create(&role)
	p1 := seedRoleHandlerPermission(t, db, "roles", "manage")

	doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), map[string]any{
		"permission_ids": []uint{p1.ID},
	}))
	doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), map[string]any{"name": "Auditor 2"}))
	doRequest(t, app, jsonRequest(t, "DELETE", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), nil))

	var reloaded database.SuperAdminUser
	if err := db.First(&reloaded, admin.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.Role != "superadmin" {
		t.Fatalf("SuperAdminUser.Role fue alterado: %q", reloaded.Role)
	}
	if reloaded.RoleID != nil {
		t.Fatalf("SuperAdminUser.RoleID fue alterado: %v", reloaded.RoleID)
	}
}

// 16. Verificar que el RBAC de tenants permanece intacto.
func TestSARoleHandler_DoesNotTouchTenantRBAC(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestApp(db)

	tenantRole := database.TenantRole{Name: "Vendedor"}
	db.Create(&tenantRole)
	tenantPerm := database.TenantPermission{Module: "sales", Action: "view", Label: "Ver ventas"}
	db.Create(&tenantPerm)
	db.Create(&database.TenantRolePermission{RoleID: tenantRole.ID, PermissionID: tenantPerm.ID})

	doRequest(t, app, jsonRequest(t, "POST", "/api/superadmin/roles", map[string]any{"name": "Vendedor Central"}))
	doRequest(t, app, jsonRequest(t, "GET", "/api/superadmin/roles", nil))
	doRequest(t, app, jsonRequest(t, "GET", "/api/superadmin/permissions", nil))

	var tRoleCount, tPermCount, tRPCount int64
	db.Model(&database.TenantRole{}).Count(&tRoleCount)
	db.Model(&database.TenantPermission{}).Count(&tPermCount)
	db.Model(&database.TenantRolePermission{}).Count(&tRPCount)
	if tRoleCount != 1 || tPermCount != 1 || tRPCount != 1 {
		t.Fatalf("el RBAC de tenant fue alterado: roles=%d permisos=%d relaciones=%d (esperado 1,1,1)",
			tRoleCount, tPermCount, tRPCount)
	}
}
