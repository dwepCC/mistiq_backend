package handler

// Fase 5, etapa 3, Grupo 7 (Paso C — Roles): tests del techo de delegación
// (middleware.CanDelegateAll) dentro de SARoleHandler y de la auditoría nueva
// (role_created/role_updated/role_deleted/role_permissions_changed). La autorización de ruta
// (RequireSAPermission) ya tiene su propia cobertura en route_wiring_test.go
// (TestProtectedRoutesGrupo7Roles_*) — aquí se prueba exclusivamente la SEGUNDA capa: qué puede
// delegar un actor que YA tiene el permiso de ruta (roles.manage/roles.delete), no si lo tiene.
//
// Usa newSARoleTestAppWithClaims (sa_role_handler_test.go, mismo paquete) para inyectar un actor
// con un conjunto de permisos explícito, sin pasar por JWT/SuperAdminAuthAPI (ya cubierto aparte).

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func nonSuperadminClaims(userID uint, permissions []string) *middleware.SuperAdminClaims {
	return &middleware.SuperAdminClaims{UserID: userID, Role: "admin", Permissions: permissions}
}

// ==================== SetRolePermissionsAPI — techo de delegación ====================

// "Caso crítico" del diseño aprobado (Grupo 7 §6): un actor con roles.manage pero SIN
// usuarios_central.change_role no puede agregar ese permiso a ningún rol.
func TestSARoleHandler_SetRolePermissionsAPI_CannotAddPermissionActorDoesNotHave(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	changeRole := seedRoleHandlerPermission(t, db, "usuarios_central", "change_role")
	role := database.SARole{Name: "Soporte"}
	db.Create(&role)

	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(7, []string{"roles.manage"}))
	resp, out := doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), map[string]any{
		"permission_ids": []uint{changeRole.ID},
	}))
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%v", resp.StatusCode, out)
	}

	var count int64
	db.Model(&database.SARolePermission{}).Where("role_id = ?", role.ID).Count(&count)
	if count != 0 {
		t.Fatalf("el permiso no delegable no debió aplicarse: quedaron %d relaciones", count)
	}
}

// Quitar un permiso que el actor NO posee está permitido — nunca es una escalada.
func TestSARoleHandler_SetRolePermissionsAPI_CanRemovePermissionActorDoesNotHave(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	legacyPerm := seedRoleHandlerPermission(t, db, "empresas", "master_access") // el actor NO tendrá este
	role := database.SARole{Name: "Legado"}
	db.Create(&role)
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: legacyPerm.ID})

	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(7, []string{"roles.manage"}))
	resp, out := doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), map[string]any{
		"permission_ids": []uint{}, // lo quita
	}))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("quitar un permiso que el actor no posee debería permitirse: status = %d, body=%v", resp.StatusCode, out)
	}

	var count int64
	db.Model(&database.SARolePermission{}).Where("role_id = ?", role.ID).Count(&count)
	if count != 0 {
		t.Fatal("el permiso debió quedar removido")
	}
}

// Mantener sin cambios un permiso que el actor no posee NO es una escalada — el actor puede
// seguir editando el resto del rol (agregar algo que SÍ posee) sin que ese permiso preexistente
// bloquee la operación.
func TestSARoleHandler_SetRolePermissionsAPI_KeepingExistingPermissionActorLacksIsNotEscalation(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	legacyPerm := seedRoleHandlerPermission(t, db, "empresas", "master_access") // el actor NO lo tiene
	ownedPerm := seedRoleHandlerPermission(t, db, "empresas", "view")           // el actor SÍ lo tiene
	role := database.SARole{Name: "Mixto"}
	db.Create(&role)
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: legacyPerm.ID})

	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(7, []string{"roles.manage", "empresas.view"}))
	resp, out := doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), map[string]any{
		// Conserva legacyPerm (sin tocar) y agrega ownedPerm (que sí posee).
		"permission_ids": []uint{legacyPerm.ID, ownedPerm.ID},
	}))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("mantener un permiso preexistente no debería bloquear la operación: status = %d, body=%v", resp.StatusCode, out)
	}

	var count int64
	db.Model(&database.SARolePermission{}).Where("role_id = ?", role.ID).Count(&count)
	if count != 2 {
		t.Fatalf("deberían quedar 2 permisos (legacyPerm conservado + ownedPerm agregado), hay %d", count)
	}
}

// Agregar un permiso que el actor SÍ posee funciona con normalidad.
func TestSARoleHandler_SetRolePermissionsAPI_CanAddPermissionActorHas(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	perm := seedRoleHandlerPermission(t, db, "empresas", "view")
	role := database.SARole{Name: "Soporte"}
	db.Create(&role)

	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(7, []string{"roles.manage", "empresas.view"}))
	resp, out := doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), map[string]any{
		"permission_ids": []uint{perm.ID},
	}))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, body=%v", resp.StatusCode, out)
	}
}

// El bypass de superadmin real ignora el techo de delegación por completo.
func TestSARoleHandler_SetRolePermissionsAPI_SuperadminBypassesDelegationCeiling(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	perm := seedRoleHandlerPermission(t, db, "usuarios_central", "change_role")
	role := database.SARole{Name: "Cualquiera"}
	db.Create(&role)

	app := newSARoleTestAppWithClaims(db, &middleware.SuperAdminClaims{UserID: 1, Role: "superadmin"})
	resp, out := doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), map[string]any{
		"permission_ids": []uint{perm.ID},
	}))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("superadmin real debería poder delegar cualquier permiso: status = %d, body=%v", resp.StatusCode, out)
	}
}

// ==================== DeleteAPI — techo de delegación ====================

// Un actor no puede eliminar un rol que contiene un permiso que él mismo no puede delegar —
// evita usar roles.manage/roles.delete para "deshacerse indirectamente" de un rol cuyo alcance
// el actor no controla.
func TestSARoleHandler_DeleteAPI_CannotDeleteRoleWithPermissionActorLacks(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	perm := seedRoleHandlerPermission(t, db, "usuarios_central", "destroy")
	role := database.SARole{Name: "Peligroso"}
	db.Create(&role)
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: perm.ID})

	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(7, []string{"roles.manage"}))
	resp, out := doRequest(t, app, jsonRequest(t, "DELETE", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), nil))
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%v", resp.StatusCode, out)
	}

	var count int64
	db.Model(&database.SARole{}).Where("id = ?", role.ID).Count(&count)
	if count != 1 {
		t.Fatal("el rol NO debió eliminarse")
	}
}

// Un actor puede eliminar un rol cuyos permisos están totalmente contenidos en los suyos.
func TestSARoleHandler_DeleteAPI_CanDeleteRoleWithinOwnCeiling(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	perm := seedRoleHandlerPermission(t, db, "empresas", "view")
	role := database.SARole{Name: "Inofensivo"}
	db.Create(&role)
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: perm.ID})

	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(7, []string{"roles.manage", "empresas.view"}))
	resp, out := doRequest(t, app, jsonRequest(t, "DELETE", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, body=%v", resp.StatusCode, out)
	}
}

// Un rol vacío (sin ningún permiso asignado) siempre puede eliminarse — el conjunto vacío
// siempre pasa CanDelegateAll.
func TestSARoleHandler_DeleteAPI_EmptyRoleAlwaysDeletable(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	role := database.SARole{Name: "Vacío"}
	db.Create(&role)

	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(7, []string{"roles.delete"}))
	resp, out := doRequest(t, app, jsonRequest(t, "DELETE", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, body=%v", resp.StatusCode, out)
	}
}

// ==================== UpdateAPI — no es una puerta trasera al techo de delegación ====================

// Modificar nombre/descripción de un rol NUNCA toca permisos, así que ni siquiera un actor cuyo
// techo de delegación no cubre el contenido del rol queda bloqueado para renombrarlo — la regla
// de delegación es sobre PERMISOS, no sobre el rol como recurso genérico, y UpdateAPI no expone
// ninguna vía para tocar sa_role_permissions.
func TestSARoleHandler_UpdateAPI_NameChangeIgnoresDelegationCeiling(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	perm := seedRoleHandlerPermission(t, db, "usuarios_central", "destroy") // el actor NO lo tiene
	role := database.SARole{Name: "Con Privilegios"}
	db.Create(&role)
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: perm.ID})

	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(7, []string{"roles.update"}))
	resp, out := doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), map[string]any{
		"name": "Renombrado", "description": "sin tocar permisos",
	}))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, body=%v", resp.StatusCode, out)
	}

	// Los permisos del rol deben seguir intactos — UpdateAPI no los tocó.
	var count int64
	db.Model(&database.SARolePermission{}).Where("role_id = ? AND permission_id = ?", role.ID, perm.ID).Count(&count)
	if count != 1 {
		t.Fatal("UpdateAPI no debió alterar los permisos del rol")
	}
}

// ==================== Auditoría ====================

func TestSARoleHandler_CreateAPI_WritesAuditLog(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(42, []string{"roles.create"}))

	doRequest(t, app, jsonRequest(t, "POST", "/api/superadmin/roles", map[string]any{
		"name": "Auditado", "description": "prueba",
	}))

	var log database.AuditLog
	if err := db.Where("action = ?", "role_created").First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
	if log.UserID != 42 {
		t.Errorf("UserID = %d, want 42", log.UserID)
	}
	if log.Entity != "sa_role" {
		t.Errorf("Entity = %q, want sa_role", log.Entity)
	}
}

func TestSARoleHandler_UpdateAPI_WritesAuditLogWithBeforeAfter(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	role := database.SARole{Name: "Original", Description: "antes"}
	db.Create(&role)

	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(42, []string{"roles.update"}))
	doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), map[string]any{
		"name": "Nuevo Nombre", "description": "después",
	}))

	var log database.AuditLog
	if err := db.Where("action = ? AND entity_id = ?", "role_updated", role.ID).First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
	if log.Payload == "" {
		t.Fatal("el payload de auditoría no debería estar vacío")
	}
}

func TestSARoleHandler_DeleteAPI_WritesAuditLogWithPermissions(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	perm := seedRoleHandlerPermission(t, db, "empresas", "view")
	role := database.SARole{Name: "A Eliminar"}
	db.Create(&role)
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: perm.ID})

	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(42, []string{"roles.delete", "empresas.view"}))
	doRequest(t, app, jsonRequest(t, "DELETE", fmt.Sprintf("/api/superadmin/roles/%d", role.ID), nil))

	var log database.AuditLog
	if err := db.Where("action = ?", "role_deleted").First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
}

func TestSARoleHandler_SetRolePermissionsAPI_WritesAuditLogWithAddedAndRemoved(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	p1 := seedRoleHandlerPermission(t, db, "empresas", "view")
	p2 := seedRoleHandlerPermission(t, db, "fiscal", "view")
	role := database.SARole{Name: "Rol"}
	db.Create(&role)
	db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: p1.ID})

	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(42, []string{"roles.manage", "fiscal.view"}))
	resp, out := doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), map[string]any{
		"permission_ids": []uint{p2.ID}, // quita p1, agrega p2
	}))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, body=%v", resp.StatusCode, out)
	}

	var log database.AuditLog
	if err := db.Where("action = ?", "role_permissions_changed").First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
	if log.Payload == "" {
		t.Fatal("el payload de auditoría (added/removed) no debería estar vacío")
	}
}

// Una operación RECHAZADA por el techo de delegación no debe dejar ningún AuditLog — auditar solo
// lo que realmente ocurrió.
func TestSARoleHandler_SetRolePermissionsAPI_RejectedDelegationDoesNotWriteAuditLog(t *testing.T) {
	db := setupSARoleHandlerTestDB(t)
	perm := seedRoleHandlerPermission(t, db, "usuarios_central", "change_role")
	role := database.SARole{Name: "Rol"}
	db.Create(&role)

	app := newSARoleTestAppWithClaims(db, nonSuperadminClaims(42, []string{"roles.manage"}))
	doRequest(t, app, jsonRequest(t, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", role.ID), map[string]any{
		"permission_ids": []uint{perm.ID},
	}))

	var count int64
	db.Model(&database.AuditLog{}).Count(&count)
	if count != 0 {
		t.Fatalf("una operación rechazada por el techo de delegación no debió auditarse: %d filas", count)
	}
}
