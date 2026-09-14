package service

import (
	"errors"
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupSARoleServiceTestDB sigue el mismo patrón que setupPlanRefTestDB (sqlite en memoria,
// cache compartido por nombre de test) ya usado en este paquete — sin archivos en disco, sin
// problemas de locking.
func setupSARoleServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.SuperAdminUser{}, &database.SARole{}, &database.SAPermission{}, &database.SARolePermission{},
		&database.TenantRole{}, &database.TenantPermission{}, &database.TenantRolePermission{}, &database.TenantUser{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedSAPermission(t *testing.T, db *gorm.DB, module, action string) database.SAPermission {
	t.Helper()
	p := database.SAPermission{Module: module, Action: action, Label: module + "." + action}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// 1. Crear rol correctamente.
func TestSARoleService_Create(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	role, err := svc.Create("Auditor", "Solo lectura para auditoría externa")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if role.ID == 0 {
		t.Fatal("el rol creado no tiene ID")
	}
	if role.IsSystem {
		t.Fatal("un rol creado vía servicio nunca debe quedar como IsSystem=true")
	}
	if role.Name != "Auditor" || role.Description != "Solo lectura para auditoría externa" {
		t.Fatalf("rol creado con datos incorrectos: %+v", role)
	}
}

// 2. No permitir nombres duplicados.
func TestSARoleService_Create_RejectsDuplicateName(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	if _, err := svc.Create("Auditor", "primero"); err != nil {
		t.Fatalf("primer Create: %v", err)
	}
	if _, err := svc.Create("Auditor", "segundo"); err == nil {
		t.Fatal("se esperaba error por nombre duplicado")
	}

	var count int64
	db.Model(&database.SARole{}).Where("name = ?", "Auditor").Count(&count)
	if count != 1 {
		t.Fatalf("se crearon %d roles con el mismo nombre, esperado 1", count)
	}
}

// 3. Obtener/listar roles.
func TestSARoleService_ListAndGetByID(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	r1, _ := svc.Create("Zeta", "")
	r2, _ := svc.Create("Alfa", "")

	roles, err := svc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(roles) != 2 {
		t.Fatalf("List devolvió %d roles, esperado 2", len(roles))
	}
	// Orden alfabético por nombre.
	if roles[0].Name != "Alfa" || roles[1].Name != "Zeta" {
		t.Fatalf("List no está ordenado por nombre: %+v", roles)
	}

	got, err := svc.GetByID(r1.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Zeta" {
		t.Fatalf("GetByID devolvió el rol equivocado: %+v", got)
	}

	if _, err := svc.GetByID(r2.ID + 9999); err == nil {
		t.Fatal("se esperaba error al buscar un rol inexistente")
	}
}

// 4. Actualizar rol.
func TestSARoleService_Update(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	role, _ := svc.Create("Auditor", "desc original")
	if err := svc.Update(role.ID, "Auditor Senior", "desc nueva"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := svc.GetByID(role.ID)
	if got.Name != "Auditor Senior" || got.Description != "desc nueva" {
		t.Fatalf("Update no aplicó los cambios: %+v", got)
	}
}

// 5. Eliminar rol personalizado.
func TestSARoleService_Delete_CustomRole(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	role, _ := svc.Create("Temporal", "")
	if err := svc.Delete(role.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.GetByID(role.ID); err == nil {
		t.Fatal("el rol debería haber sido eliminado")
	}
}

// 6. No permitir eliminar rol de sistema.
func TestSARoleService_Delete_RejectsSystemRole(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	// Los roles de sistema los crea el seed, no el servicio — se simula aquí como lo hace el seed.
	sysRole := database.SARole{Name: "Admin", Description: "rol de sistema", IsSystem: true}
	if err := db.Create(&sysRole).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.Delete(sysRole.ID); err == nil {
		t.Fatal("se esperaba error al eliminar un rol de sistema")
	}
	if _, err := svc.GetByID(sysRole.ID); err != nil {
		t.Fatal("el rol de sistema no debería haber sido eliminado")
	}
}

// Además: no se puede eliminar un rol (aunque no sea de sistema) si tiene usuarios asignados.
func TestSARoleService_Delete_RejectsRoleWithAssignedUsers(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	role, _ := svc.Create("Finanzas Jr", "")
	user := database.SuperAdminUser{Name: "Ana", Email: "ana@example.com", Role: "admin", RoleID: &role.ID}
	if err := user.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.Delete(role.ID); err == nil {
		t.Fatal("se esperaba error al eliminar un rol con usuarios asignados")
	}
}

// 7. Asignar permisos.
func TestSARoleService_SetRolePermissions_Assign(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	role, _ := svc.Create("Soporte Jr", "")
	pView := seedSAPermission(t, db, "empresas", "view")
	pFiscal := seedSAPermission(t, db, "fiscal", "view")

	if err := svc.SetRolePermissions(role.ID, []uint{pView.ID, pFiscal.ID}); err != nil {
		t.Fatalf("SetRolePermissions: %v", err)
	}

	ids, err := svc.RolePermissions(role.ID)
	if err != nil {
		t.Fatalf("RolePermissions: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("se asignaron %d permisos, esperado 2", len(ids))
	}
}

// 8. Reemplazar permisos.
func TestSARoleService_SetRolePermissions_Replace(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	role, _ := svc.Create("Soporte Jr", "")
	pView := seedSAPermission(t, db, "empresas", "view")
	pFiscal := seedSAPermission(t, db, "fiscal", "view")
	pMigra := seedSAPermission(t, db, "migraciones", "view")

	if err := svc.SetRolePermissions(role.ID, []uint{pView.ID, pFiscal.ID}); err != nil {
		t.Fatalf("primer SetRolePermissions: %v", err)
	}
	// Reemplazo total: quita pFiscal, agrega pMigra.
	if err := svc.SetRolePermissions(role.ID, []uint{pView.ID, pMigra.ID}); err != nil {
		t.Fatalf("segundo SetRolePermissions: %v", err)
	}

	keys, err := svc.GetRolePermissionKeys(role.ID)
	if err != nil {
		t.Fatalf("GetRolePermissionKeys: %v", err)
	}
	want := map[string]bool{"empresas.view": true, "migraciones.view": true}
	if len(keys) != len(want) {
		t.Fatalf("permisos tras reemplazo = %v, esperado %v", keys, want)
	}
	for _, k := range keys {
		if !want[k] {
			t.Fatalf("permiso inesperado tras reemplazo: %s (no se limpiaron los anteriores)", k)
		}
	}
}

// 9. El reemplazo es atómico: una llamada rechazada no deja estado parcial (los permisos previos
// del rol permanecen exactamente iguales).
func TestSARoleService_SetRolePermissions_NoPartialStateOnFailure(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	role, _ := svc.Create("Soporte Jr", "")
	pView := seedSAPermission(t, db, "empresas", "view")
	pFiscal := seedSAPermission(t, db, "fiscal", "view")

	if err := svc.SetRolePermissions(role.ID, []uint{pView.ID, pFiscal.ID}); err != nil {
		t.Fatalf("setup SetRolePermissions: %v", err)
	}

	nonExistentID := pFiscal.ID + 99999
	err := svc.SetRolePermissions(role.ID, []uint{pView.ID, nonExistentID})
	if err == nil {
		t.Fatal("se esperaba error por permiso inexistente")
	}

	// El estado debe seguir siendo EXACTAMENTE el de antes de la llamada fallida — ni se agregó
	// el ID inexistente, ni se perdió pFiscal (que la llamada fallida intentaba quitar).
	ids, err := svc.RolePermissions(role.ID)
	if err != nil {
		t.Fatalf("RolePermissions: %v", err)
	}
	got := map[uint]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if len(got) != 2 || !got[pView.ID] || !got[pFiscal.ID] {
		t.Fatalf("el estado quedó parcialmente modificado tras una llamada fallida: %v", ids)
	}
}

// 10. No permitir permiso inexistente.
func TestSARoleService_SetRolePermissions_RejectsNonExistentPermission(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	role, _ := svc.Create("Soporte Jr", "")
	err := svc.SetRolePermissions(role.ID, []uint{999999})
	if err == nil {
		t.Fatal("se esperaba error por permiso inexistente")
	}

	var count int64
	db.Model(&database.SARolePermission{}).Where("role_id = ?", role.ID).Count(&count)
	if count != 0 {
		t.Fatalf("no debió crearse ninguna relación, se encontraron %d", count)
	}
}

// Duplicados en el input se deduplican silenciosamente (no rompen la relación PK compuesta).
func TestSARoleService_SetRolePermissions_DedupesInput(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	role, _ := svc.Create("Soporte Jr", "")
	pView := seedSAPermission(t, db, "empresas", "view")

	if err := svc.SetRolePermissions(role.ID, []uint{pView.ID, pView.ID, pView.ID}); err != nil {
		t.Fatalf("SetRolePermissions con IDs duplicados: %v", err)
	}

	var count int64
	db.Model(&database.SARolePermission{}).Where("role_id = ?", role.ID).Count(&count)
	if count != 1 {
		t.Fatalf("se crearon %d relaciones para un permiso duplicado, esperado 1", count)
	}
}

// 11. No crear ni manipular un rol "Superadmin" (ninguna variación de mayúsculas/minúsculas).
func TestSARoleService_RejectsSuperadminRoleName(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	for _, name := range []string{"Superadmin", "superadmin", "SUPERADMIN", "SuperAdmin"} {
		if _, err := svc.Create(name, "intento de crear bypass falso"); err == nil {
			t.Fatalf("se esperaba error al crear un rol llamado %q", name)
		}
	}

	var count int64
	db.Model(&database.SARole{}).Where("LOWER(name) = ?", "superadmin").Count(&count)
	if count != 0 {
		t.Fatalf("no debe existir ninguna fila SARole con nombre 'superadmin' (en cualquier variación), se encontraron %d", count)
	}

	// Tampoco se puede renombrar un rol existente a "Superadmin".
	role, err := svc.Create("Casi Admin", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Update(role.ID, "SuperAdmin", ""); err == nil {
		t.Fatal("se esperaba error al renombrar un rol a 'SuperAdmin'")
	}
}

// Este servicio nunca debe tocar SuperAdminUser.Role (el bypass real) ni SuperAdminUser.RoleID —
// ninguno de sus métodos escribe en la tabla superadmin_users.
func TestSARoleService_NeverTouchesSuperAdminUserTable(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	admin := database.SuperAdminUser{Name: "Real Admin", Email: "real@example.com", Role: "superadmin"}
	if err := admin.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}

	role, _ := svc.Create("Casi Admin", "")
	perm := seedSAPermission(t, db, "roles", "manage")
	_ = svc.SetRolePermissions(role.ID, []uint{perm.ID})
	_ = svc.Update(role.ID, "Casi Admin 2", "")
	_ = svc.Delete(role.ID)

	var reloaded database.SuperAdminUser
	if err := db.First(&reloaded, admin.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.Role != "superadmin" {
		t.Fatalf("SuperAdminUser.Role fue alterado inesperadamente: %q", reloaded.Role)
	}
	if reloaded.RoleID != nil {
		t.Fatalf("SuperAdminUser.RoleID fue alterado inesperadamente: %v", reloaded.RoleID)
	}
}

// 12. Los permisos críticos siguen siendo independientes de su ".manage": asignar solo
// "documentos.manage" a un rol NO debe otorgar implícitamente "documentos.approve_purchase" (ni
// viceversa) a nivel de este servicio — la expansión de ".manage" no existe en esta capa (queda
// para el middleware en la Fase 5, y ahí también deberá excluir explícitamente estas acciones).
func TestSARoleService_CriticalActionsStayIndependentFromManage(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	docManage := seedSAPermission(t, db, "documentos", "manage")
	docApprove := seedSAPermission(t, db, "documentos", "approve_purchase")
	fiscalRetry := seedSAPermission(t, db, "fiscal", "retry")
	fiscalCancel := seedSAPermission(t, db, "fiscal", "cancel")

	role, _ := svc.Create("Solo Manage", "")
	if err := svc.SetRolePermissions(role.ID, []uint{docManage.ID}); err != nil {
		t.Fatalf("SetRolePermissions: %v", err)
	}

	keys, err := svc.GetRolePermissionKeys(role.ID)
	if err != nil {
		t.Fatalf("GetRolePermissionKeys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "documentos.manage" {
		t.Fatalf("documentos.manage otorgó permisos adicionales de forma implícita: %v", keys)
	}

	// Confirma también que fiscal.retry y fiscal.cancel son filas de catálogo independientes
	// (decisión aprobada #1) — no una sola entrada combinada.
	if docApprove.ID == docManage.ID {
		t.Fatal("documentos.manage y documentos.approve_purchase deben ser permisos distintos")
	}
	if fiscalRetry.ID == fiscalCancel.ID {
		t.Fatal("fiscal.retry y fiscal.cancel deben ser permisos distintos")
	}
}

// 13. El servicio del RBAC central no toca ni se cruza con el RBAC de tenants.
func TestSARoleService_DoesNotTouchTenantRBAC(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	tenantRole := database.TenantRole{Name: "Vendedor"}
	if err := db.Create(&tenantRole).Error; err != nil {
		t.Fatal(err)
	}
	tenantPerm := database.TenantPermission{Module: "sales", Action: "view", Label: "Ver ventas"}
	if err := db.Create(&tenantPerm).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantRolePermission{RoleID: tenantRole.ID, PermissionID: tenantPerm.ID}).Error; err != nil {
		t.Fatal(err)
	}

	role, _ := svc.Create("Vendedor", "mismo nombre que el rol de tenant, tablas distintas")
	perm := seedSAPermission(t, db, "sales", "view") // mismo module.action que el permiso de tenant
	_ = svc.SetRolePermissions(role.ID, []uint{perm.ID})
	_ = svc.Update(role.ID, "Vendedor Central", "")

	var tRoleCount, tPermCount, tRPCount int64
	db.Model(&database.TenantRole{}).Count(&tRoleCount)
	db.Model(&database.TenantPermission{}).Count(&tPermCount)
	db.Model(&database.TenantRolePermission{}).Count(&tRPCount)
	if tRoleCount != 1 || tPermCount != 1 || tRPCount != 1 {
		t.Fatalf("el RBAC de tenant fue alterado: roles=%d permisos=%d relaciones=%d (esperado 1,1,1)",
			tRoleCount, tPermCount, tRPCount)
	}

	var tRole database.TenantRole
	if err := db.First(&tRole, tenantRole.ID).Error; err != nil {
		t.Fatal(err)
	}
	if tRole.Name != "Vendedor" {
		t.Fatalf("el rol de tenant fue modificado: %+v", tRole)
	}
}

// Casos de validación adicionales exigidos: nombre obligatorio y estados inválidos.
func TestSARoleService_Create_RejectsEmptyName(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	if _, err := svc.Create("   ", ""); err == nil {
		t.Fatal("se esperaba error por nombre vacío")
	}
}

func TestSARoleService_GetByID_NotFoundIsGormError(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	_, err := svc.GetByID(12345)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("esperaba gorm.ErrRecordNotFound, obtuvo: %v", err)
	}
}

// Fase 4 — cambiar los permisos de un rol invalida (TokenVersion++) la sesión de todo usuario
// que tenga ese rol asignado, y no toca a usuarios de otros roles.
func TestSARoleService_SetRolePermissions_InvalidatesSessionsOfUsersWithThatRole(t *testing.T) {
	db := setupSARoleServiceTestDB(t)
	svc := NewSARoleService(db)

	roleA, _ := svc.Create("Soporte Jr", "")
	roleB, _ := svc.Create("Otro Rol", "")
	perm := seedSAPermission(t, db, "empresas", "view")

	userA1 := database.SuperAdminUser{Name: "A1", Email: "a1@example.com", Role: "admin", RoleID: &roleA.ID, TokenVersion: 3}
	userA2 := database.SuperAdminUser{Name: "A2", Email: "a2@example.com", Role: "admin", RoleID: &roleA.ID, TokenVersion: 0}
	userB := database.SuperAdminUser{Name: "B1", Email: "b1@example.com", Role: "admin", RoleID: &roleB.ID, TokenVersion: 5}
	for _, u := range []*database.SuperAdminUser{&userA1, &userA2, &userB} {
		if err := u.SetPassword("password123"); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := svc.SetRolePermissions(roleA.ID, []uint{perm.ID}); err != nil {
		t.Fatalf("SetRolePermissions: %v", err)
	}

	var reloadedA1, reloadedA2, reloadedB database.SuperAdminUser
	db.First(&reloadedA1, userA1.ID)
	db.First(&reloadedA2, userA2.ID)
	db.First(&reloadedB, userB.ID)

	if reloadedA1.TokenVersion != 4 {
		t.Fatalf("userA1.TokenVersion = %d, esperado 4 (invalidado)", reloadedA1.TokenVersion)
	}
	if reloadedA2.TokenVersion != 1 {
		t.Fatalf("userA2.TokenVersion = %d, esperado 1 (invalidado)", reloadedA2.TokenVersion)
	}
	if reloadedB.TokenVersion != 5 {
		t.Fatalf("userB.TokenVersion = %d, esperado 5 (sin cambios — tiene otro rol)", reloadedB.TokenVersion)
	}
}
