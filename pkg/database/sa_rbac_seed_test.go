package database

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupSARBACTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// Archivo único por test (vía t.TempDir, limpiado automáticamente incluido -wal/-shm) para
	// que no queden residuos de WAL contaminando el siguiente test dentro del mismo paquete.
	dbPath := filepath.Join(t.TempDir(), "sa_rbac_seed_test.db")
	dsn := "file:" + dbPath + "?_journal_mode=WAL&_busy_timeout=15000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Cierra la conexión antes de que t.TempDir() intente borrar el directorio — en Windows no
	// se puede eliminar un archivo sqlite todavía abierto.
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	// Migra también el RBAC de tenants en la misma BD de prueba, para poder verificar en el
	// mismo test que sembrar el RBAC central no lo toca ni lo rompe.
	if err := db.AutoMigrate(
		&SuperAdminUser{}, &SARole{}, &SAPermission{}, &SARolePermission{},
		&TenantRole{}, &TenantPermission{}, &TenantRolePermission{}, &TenantUser{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestSASeedRolesAndPermissions_CreatesCatalogAndRoles(t *testing.T) {
	db := setupSARBACTestDB(t)

	if err := SASeedRolesAndPermissions(db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var permCount int64
	db.Model(&SAPermission{}).Count(&permCount)
	if int(permCount) != len(SACentralPermissionCatalog) {
		t.Fatalf("permisos creados = %d, esperado %d", permCount, len(SACentralPermissionCatalog))
	}

	var roleCount int64
	db.Model(&SARole{}).Count(&roleCount)
	if int(roleCount) != len(SADefaultRoles) {
		t.Fatalf("roles creados = %d, esperado %d", roleCount, len(SADefaultRoles))
	}

	// No debe existir una fila "Superadmin" en sa_roles — es bypass puro por SuperAdminUser.Role.
	var superadminRoleCount int64
	db.Model(&SARole{}).Where("name = ?", "Superadmin").Count(&superadminRoleCount)
	if superadminRoleCount != 0 {
		t.Fatalf("no debería existir una fila SARole 'Superadmin' (bypass puro), se encontraron %d", superadminRoleCount)
	}

	// Verifica que cada rol quedó con exactamente los permisos declarados en SADefaultRoles.
	for _, def := range SADefaultRoles {
		var role SARole
		if err := db.Where("name = ?", def.Name).First(&role).Error; err != nil {
			t.Fatalf("rol %s no encontrado: %v", def.Name, err)
		}
		var rpCount int64
		db.Model(&SARolePermission{}).Where("role_id = ?", role.ID).Count(&rpCount)
		if int(rpCount) != len(def.Permissions) {
			t.Fatalf("rol %s: %d permisos asignados, esperado %d", def.Name, rpCount, len(def.Permissions))
		}

		keys, err := saTestRolePermissionKeys(db, role.ID)
		if err != nil {
			t.Fatalf("rol %s: error leyendo permisos: %v", def.Name, err)
		}
		want := map[string]bool{}
		for _, k := range def.Permissions {
			want[k] = true
		}
		for _, k := range keys {
			if !want[k] {
				t.Fatalf("rol %s: tiene el permiso inesperado %q", def.Name, k)
			}
			delete(want, k)
		}
		if len(want) != 0 {
			t.Fatalf("rol %s: faltan permisos %v", def.Name, want)
		}
	}

	// empresas.destroy NO debe existir como permiso otorgable (reservado a bypass superadmin).
	var destroyPermCount int64
	db.Model(&SAPermission{}).Where("module = ? AND action = ?", "empresas", "destroy").Count(&destroyPermCount)
	if destroyPermCount != 0 {
		t.Fatalf("empresas.destroy no debe existir en el catálogo de permisos otorgables")
	}
}

func TestSASeedRolesAndPermissions_IsIdempotent(t *testing.T) {
	db := setupSARBACTestDB(t)

	if err := SASeedRolesAndPermissions(db); err != nil {
		t.Fatalf("primer seed: %v", err)
	}
	if err := SASeedRolesAndPermissions(db); err != nil {
		t.Fatalf("segundo seed: %v", err)
	}
	if err := SASeedRolesAndPermissions(db); err != nil {
		t.Fatalf("tercer seed: %v", err)
	}

	var permCount int64
	db.Model(&SAPermission{}).Count(&permCount)
	if int(permCount) != len(SACentralPermissionCatalog) {
		t.Fatalf("permisos duplicados tras reseeding: %d, esperado %d", permCount, len(SACentralPermissionCatalog))
	}

	var roleCount int64
	db.Model(&SARole{}).Count(&roleCount)
	if int(roleCount) != len(SADefaultRoles) {
		t.Fatalf("roles duplicados tras reseeding: %d, esperado %d", roleCount, len(SADefaultRoles))
	}

	var rpCount int64
	db.Model(&SARolePermission{}).Count(&rpCount)
	wantRP := 0
	for _, def := range SADefaultRoles {
		wantRP += len(def.Permissions)
	}
	if int(rpCount) != wantRP {
		t.Fatalf("relaciones rol-permiso duplicadas tras reseeding: %d, esperado %d", rpCount, wantRP)
	}
}

func TestSASeedRolesAndPermissions_DoesNotOverwriteCustomizedRole(t *testing.T) {
	db := setupSARBACTestDB(t)

	if err := SASeedRolesAndPermissions(db); err != nil {
		t.Fatalf("primer seed: %v", err)
	}

	// Simula que un superadmin editó los permisos del rol "Soporte" desde la futura pantalla de
	// roles (le agregó un permiso que no estaba en el catálogo inicial).
	var soporte SARole
	if err := db.Where("name = ?", "Soporte").First(&soporte).Error; err != nil {
		t.Fatalf("rol Soporte no encontrado: %v", err)
	}
	var extraPerm SAPermission
	if err := db.Where("module = ? AND action = ?", "pagos", "view").First(&extraPerm).Error; err != nil {
		t.Fatalf("permiso pagos.view no encontrado: %v", err)
	}
	if err := db.Create(&SARolePermission{RoleID: soporte.ID, PermissionID: extraPerm.ID}).Error; err != nil {
		t.Fatalf("no se pudo simular la personalización: %v", err)
	}

	// Reseeding (ej. tras un redeploy) no debe pisar esa personalización.
	if err := SASeedRolesAndPermissions(db); err != nil {
		t.Fatalf("segundo seed: %v", err)
	}

	var rpCount int64
	db.Model(&SARolePermission{}).Where("role_id = ?", soporte.ID).Count(&rpCount)
	wantCount := len(func() []string {
		for _, def := range SADefaultRoles {
			if def.Name == "Soporte" {
				return def.Permissions
			}
		}
		return nil
	}()) + 1 // +1 por el permiso agregado manualmente
	if int(rpCount) != wantCount {
		t.Fatalf("el reseeding modificó los permisos personalizados del rol Soporte: %d, esperado %d", rpCount, wantCount)
	}
}

func TestSASeedRolesAndPermissions_DoesNotTouchTenantRBAC(t *testing.T) {
	db := setupSARBACTestDB(t)

	// Crea datos del RBAC de TENANT antes de sembrar el RBAC central, para comprobar que no
	// se cruzan ni se pisan entre sí.
	tenantRole := TenantRole{Name: "Vendedor", Description: "Rol de tenant preexistente"}
	if err := db.Create(&tenantRole).Error; err != nil {
		t.Fatalf("crear TenantRole: %v", err)
	}
	tenantPerm := TenantPermission{Module: "sales", Action: "view", Label: "Ver ventas"}
	if err := db.Create(&tenantPerm).Error; err != nil {
		t.Fatalf("crear TenantPermission: %v", err)
	}
	if err := db.Create(&TenantRolePermission{RoleID: tenantRole.ID, PermissionID: tenantPerm.ID}).Error; err != nil {
		t.Fatalf("crear TenantRolePermission: %v", err)
	}

	if err := SASeedRolesAndPermissions(db); err != nil {
		t.Fatalf("seed central: %v", err)
	}

	// El RBAC de tenant debe seguir exactamente igual: 1 rol, 1 permiso, 1 relación.
	var tRoleCount, tPermCount, tRPCount int64
	db.Model(&TenantRole{}).Count(&tRoleCount)
	db.Model(&TenantPermission{}).Count(&tPermCount)
	db.Model(&TenantRolePermission{}).Count(&tRPCount)
	if tRoleCount != 1 || tPermCount != 1 || tRPCount != 1 {
		t.Fatalf("el seed central alteró el RBAC de tenant: roles=%d permisos=%d relaciones=%d (esperado 1,1,1)",
			tRoleCount, tPermCount, tRPCount)
	}

	var tRole TenantRole
	if err := db.First(&tRole, tenantRole.ID).Error; err != nil {
		t.Fatalf("TenantRole desapareció: %v", err)
	}
	if tRole.Name != "Vendedor" {
		t.Fatalf("TenantRole fue modificado inesperadamente: %+v", tRole)
	}

	// Y las tablas son físicamente distintas: sa_roles no debe contener "Vendedor".
	var crossCount int64
	db.Model(&SARole{}).Where("name = ?", "Vendedor").Count(&crossCount)
	if crossCount != 0 {
		t.Fatalf("el rol de tenant se filtró a sa_roles")
	}
}

func saTestRolePermissionKeys(db *gorm.DB, roleID uint) ([]string, error) {
	var perms []SAPermission
	err := db.Table("sa_permissions").
		Select("sa_permissions.module, sa_permissions.action").
		Joins("INNER JOIN sa_role_permissions ON sa_role_permissions.permission_id = sa_permissions.id").
		Where("sa_role_permissions.role_id = ?", roleID).
		Find(&perms).Error
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(perms))
	for i, p := range perms {
		keys[i] = p.Module + "." + p.Action
	}
	return keys, nil
}
