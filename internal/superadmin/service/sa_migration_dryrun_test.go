package service

// Fase 6 — Pre-migración de usuarios reales: tests de DryRunUserRoleMigration. TODOS estos tests
// verifican, además de lo que dice cada nombre, que la BD queda exactamente igual después de
// correr el dry-run (ver TestDryRunUserRoleMigration_NeverWritesAnything) — es la propiedad más
// importante de esta fase.

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupMigrationDryRunTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.SuperAdminUser{}, &database.SARole{}, &database.SAPermission{}, &database.SARolePermission{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// seedStandardSystemRoles crea Admin/Soporte/Finanzas tal como lo haría SASeedRolesAndPermissions
// (sin depender de esa función, para no acoplar este test a su catálogo completo).
func seedStandardSystemRoles(t *testing.T, db *gorm.DB) map[string]database.SARole {
	t.Helper()
	out := map[string]database.SARole{}
	for _, name := range []string{"Admin", "Soporte", "Finanzas"} {
		r := database.SARole{Name: name, IsSystem: true}
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
		out[name] = r
	}
	return out
}

func createDryRunUser(t *testing.T, db *gorm.DB, email, role string, roleID *uint, active bool) database.SuperAdminUser {
	t.Helper()
	u := database.SuperAdminUser{Name: "T", Email: email, Role: role, RoleID: roleID, Active: true}
	if err := u.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	if !active {
		if err := db.Model(&u).UpdateColumn("active", false).Error; err != nil {
			t.Fatal(err)
		}
		u.Active = false
	}
	return u
}

func rowFor(t *testing.T, report *MigrationDryRunReport, userID uint) MigrationPlanRow {
	t.Helper()
	for _, r := range report.Rows {
		if r.UserID == userID {
			return r
		}
	}
	t.Fatalf("no se encontró la fila para el usuario %d", userID)
	return MigrationPlanRow{}
}

// 1. admin → propone RoleID Admin.
func TestDryRunUserRoleMigration_AdminProposesAdminRoleID(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true) // asegura superadmin operativo
	u := createDryRunUser(t, db, "admin@example.com", "admin", nil, true)

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	row := rowFor(t, report, u.ID)
	if row.ProposedRoleID == nil || *row.ProposedRoleID != report.RoleIDsByName["Admin"] {
		t.Fatalf("ProposedRoleID = %v, want %d (rol Admin)", row.ProposedRoleID, report.RoleIDsByName["Admin"])
	}
	if !row.HasProposal() {
		t.Fatal("HasProposal() debería ser true")
	}
}

// 2. superadmin → propone RoleID NULL.
func TestDryRunUserRoleMigration_SuperadminProposesNilRoleID(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	seedStandardSystemRoles(t, db)
	u := createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	row := rowFor(t, report, u.ID)
	if row.ProposedRoleID != nil {
		t.Fatalf("ProposedRoleID = %v, want nil", row.ProposedRoleID)
	}
	if len(row.Anomalies) != 0 {
		t.Fatalf("no debería tener anomalías: %v", row.Anomalies)
	}
	if row.HasProposal() {
		t.Fatal("HasProposal() debería ser false (ya está en nil, no hay cambio real)")
	}
}

// 3. usuario ya con RoleID → reportar conflicto, sin propuesta.
func TestDryRunUserRoleMigration_ExistingRoleIDReportsConflict(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	roles := seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)
	soporteID := roles["Soporte"].ID
	u := createDryRunUser(t, db, "admin@example.com", "admin", &soporteID, true)

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	row := rowFor(t, report, u.ID)
	if row.ProposedRoleID != nil {
		t.Fatalf("no debería proponer ningún cambio: %v", row.ProposedRoleID)
	}
	if len(row.Anomalies) == 0 {
		t.Fatal("debería reportar la anomalía de conflicto")
	}
}

// 4. Role desconocido → reportar, sin propuesta.
func TestDryRunUserRoleMigration_UnknownRoleReported(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)
	u := createDryRunUser(t, db, "raro@example.com", "moderator", nil, true)

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	row := rowFor(t, report, u.ID)
	if row.ProposedRoleID != nil {
		t.Fatal("Role desconocido no debería generar ninguna propuesta")
	}
	if len(row.Anomalies) == 0 {
		t.Fatal("debería reportar la anomalía de Role desconocido")
	}
}

// 5. usuario eliminado → reportar, sin propuesta.
func TestDryRunUserRoleMigration_DeletedUserReported(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)
	u := createDryRunUser(t, db, "borrado@example.com", "admin", nil, true)
	if err := db.Delete(&u).Error; err != nil {
		t.Fatal(err)
	}

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	row := rowFor(t, report, u.ID)
	if !row.Deleted {
		t.Fatal("Deleted debería ser true")
	}
	if row.ProposedRoleID != nil {
		t.Fatal("un usuario eliminado no debería recibir ninguna propuesta")
	}
	if len(row.Anomalies) == 0 {
		t.Fatal("debería reportar la anomalía de usuario eliminado")
	}
}

// 6. admin inactivo → reportar estado, pero SÍ sigue recibiendo la propuesta normal (no se
// cambia nada, solo se reporta el estado junto con la propuesta).
func TestDryRunUserRoleMigration_InactiveAdminReportsStatusButStillProposes(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)
	u := createDryRunUser(t, db, "admin-inactivo@example.com", "admin", nil, false)

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	row := rowFor(t, report, u.ID)
	if row.Active {
		t.Fatal("Active debería ser false")
	}
	if row.ProposedRoleID == nil {
		t.Fatal("un admin inactivo debería seguir recibiendo la propuesta de RoleID Admin")
	}
	found := false
	for _, a := range row.Anomalies {
		if a != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("debería reportar el estado inactivo como anomalía/observación")
	}
}

// 7. ningún dato cambia después del dry-run.
func TestDryRunUserRoleMigration_NeverWritesAnything(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	roles := seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)
	adminID := roles["Admin"].ID
	createDryRunUser(t, db, "admin1@example.com", "admin", nil, true)
	createDryRunUser(t, db, "admin2@example.com", "admin", &adminID, true)
	createDryRunUser(t, db, "raro@example.com", "moderator", nil, false)

	var usersBefore []database.SuperAdminUser
	if err := db.Unscoped().Order("id ASC").Find(&usersBefore).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := DryRunUserRoleMigration(db); err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}

	var usersAfter []database.SuperAdminUser
	if err := db.Unscoped().Order("id ASC").Find(&usersAfter).Error; err != nil {
		t.Fatal(err)
	}

	if len(usersBefore) != len(usersAfter) {
		t.Fatalf("cambió el número de usuarios: antes=%d después=%d", len(usersBefore), len(usersAfter))
	}
	for i := range usersBefore {
		b, a := usersBefore[i], usersAfter[i]
		if b.Role != a.Role || !roleIDEqual(b.RoleID, a.RoleID) || b.Active != a.Active ||
			b.TokenVersion != a.TokenVersion || b.DeletedAt != a.DeletedAt || b.Password != a.Password {
			t.Fatalf("el usuario %d cambió: antes=%+v después=%+v", b.ID, b, a)
		}
	}
}

// 8. Admin role se resuelve por nombre, no por ID hardcodeado.
func TestDryRunUserRoleMigration_ResolvesAdminRoleByNameNotHardcodedID(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	// Se crean primero OTROS roles para que "Admin" NO termine con ID=1 — si el código
	// asumiera ID=1, este test lo detectaría.
	db.Create(&database.SARole{Name: "Zeta"})
	db.Create(&database.SARole{Name: "Beta"})
	roles := seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)
	u := createDryRunUser(t, db, "admin@example.com", "admin", nil, true)

	adminRole := roles["Admin"]
	if adminRole.ID == 1 {
		t.Skip("el rol Admin terminó con ID=1 por casualidad del orden de inserción — no prueba nada, se salta")
	}

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	if report.RoleIDsByName["Admin"] != adminRole.ID {
		t.Fatalf("RoleIDsByName[Admin] = %d, want %d", report.RoleIDsByName["Admin"], adminRole.ID)
	}
	row := rowFor(t, report, u.ID)
	if row.ProposedRoleID == nil || *row.ProposedRoleID != adminRole.ID {
		t.Fatalf("ProposedRoleID = %v, want %d (resuelto por nombre, no por ID=1)", row.ProposedRoleID, adminRole.ID)
	}
}

// 9. ausencia de superadmin operativo → Blocked=true, la migración NO debe proceder.
func TestDryRunUserRoleMigration_NoOperationalSuperadmin_Blocked(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "admin@example.com", "admin", nil, true) // ningún superadmin

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	if !report.Blocked {
		t.Fatal("Blocked debería ser true sin ningún superadmin operativo")
	}
	if report.BlockReason == "" {
		t.Fatal("BlockReason no debería estar vacío")
	}
}

// El dry-run también se marca Blocked si el único superadmin existente está inactivo.
func TestDryRunUserRoleMigration_OnlyInactiveSuperadmin_Blocked(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, false)

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	if !report.Blocked {
		t.Fatal("Blocked debería ser true: el único superadmin existente está inactivo")
	}
	if report.Superadmins.Inactive != 1 || report.Superadmins.Active != 0 {
		t.Fatalf("Superadmins = %+v, want Active=0 Inactive=1", report.Superadmins)
	}
}

// 10. múltiples superadmins → todos quedan como Role superadmin, todos con propuesta RoleID nil.
func TestDryRunUserRoleMigration_MultipleSuperadmins_AllProposeNilRoleID(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	seedStandardSystemRoles(t, db)
	sa1 := createDryRunUser(t, db, "sa1@example.com", "superadmin", nil, true)
	sa2 := createDryRunUser(t, db, "sa2@example.com", "superadmin", nil, true)

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	for _, id := range []uint{sa1.ID, sa2.ID} {
		row := rowFor(t, report, id)
		if row.Role != "superadmin" {
			t.Fatalf("usuario %d: Role = %q, want superadmin", id, row.Role)
		}
		if row.ProposedRoleID != nil {
			t.Fatalf("usuario %d: ProposedRoleID = %v, want nil", id, row.ProposedRoleID)
		}
	}
	if report.Superadmins.Active != 2 {
		t.Fatalf("Superadmins.Active = %d, want 2", report.Superadmins.Active)
	}
	if report.Blocked {
		t.Fatal("Blocked no debería ser true: hay 2 superadmins activos")
	}
}

// ==================== Extras: catálogo del rol Admin, roles de sistema faltantes/duplicados, email duplicado ====================

func TestDryRunUserRoleMigration_ReportsAdminRolePermissions(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	roles := seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)
	perm := database.SAPermission{Module: "empresas", Action: "view", Label: "Ver empresas"}
	db.Create(&perm)
	db.Create(&database.SARolePermission{RoleID: roles["Admin"].ID, PermissionID: perm.ID})

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	if len(report.AdminRolePermissions) != 1 || report.AdminRolePermissions[0] != "empresas.view" {
		t.Fatalf("AdminRolePermissions = %v, want [empresas.view]", report.AdminRolePermissions)
	}
}

func TestDryRunUserRoleMigration_MissingSystemRoleReported(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	// Solo se crea Admin — Soporte y Finanzas faltan a propósito.
	db.Create(&database.SARole{Name: "Admin"})
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	if len(report.MissingSystemRoles) != 2 {
		t.Fatalf("MissingSystemRoles = %v, want [Soporte Finanzas]", report.MissingSystemRoles)
	}
}

func TestDryRunUserRoleMigration_DuplicateSystemRoleReported(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	// sa_roles.name tiene uniqueIndex en producción (correcto) — para simular el escenario
	// defensivo "de alguna forma existen dos filas con el mismo nombre" (p. ej. una inconsistencia
	// heredada, o el índice todavía no aplicado en un entorno viejo) se retira el índice SOLO en
	// este fixture de test. El código de DryRunUserRoleMigration no debe asumir que el esquema
	// SIEMPRE garantiza unicidad — por eso lo vuelve a verificar en memoria.
	if err := db.Migrator().DropIndex(&database.SARole{}, "Name"); err != nil {
		t.Fatalf("DropIndex: %v", err)
	}
	db.Create(&database.SARole{Name: "Admin"})
	db.Create(&database.SARole{Name: "Admin"}) // duplicado, posible solo sin el índice único
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	if len(report.DuplicateSystemRoles) != 1 || report.DuplicateSystemRoles[0] != "Admin" {
		t.Fatalf("DuplicateSystemRoles = %v, want [Admin]", report.DuplicateSystemRoles)
	}
	// Con el rol Admin ambiguo, ningún admin debería recibir propuesta.
	u := createDryRunUser(t, db, "admin@example.com", "admin", nil, true)
	report2, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	row := rowFor(t, report2, u.ID)
	if row.ProposedRoleID != nil {
		t.Fatal("con Admin duplicado, no debe proponerse ningún RoleID")
	}
}

func TestDryRunUserRoleMigration_DuplicateEmailReported(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	// super_admin_users.email tiene uniqueIndex en producción (correcto) — se retira SOLO en este
	// fixture para simular el escenario defensivo (ver comentario equivalente en
	// TestDryRunUserRoleMigration_DuplicateSystemRoleReported).
	if err := db.Migrator().DropIndex(&database.SuperAdminUser{}, "Email"); err != nil {
		t.Fatalf("DropIndex: %v", err)
	}
	seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)
	u1 := createDryRunUser(t, db, "dup@example.com", "admin", nil, true)
	u2 := database.SuperAdminUser{Name: "Otro", Email: "dup@example.com", Role: "admin", Active: true}
	if err := u2.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&u2).Error; err != nil {
		t.Fatal(err)
	}

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	for _, id := range []uint{u1.ID, u2.ID} {
		row := rowFor(t, report, id)
		found := false
		for _, a := range row.Anomalies {
			if a == "email duplicado entre varios usuarios" {
				found = true
			}
		}
		if !found {
			t.Fatalf("usuario %d debería reportar email duplicado, anomalías=%v", id, row.Anomalies)
		}
	}
}

// Un RoleID huérfano (apunta a un rol que ya no existe) debe distinguirse de uno válido.
func TestDryRunUserRoleMigration_OrphanRoleIDReported(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)
	orphanID := uint(99999)
	u := createDryRunUser(t, db, "admin@example.com", "admin", &orphanID, true)

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	row := rowFor(t, report, u.ID)
	found := false
	for _, a := range row.Anomalies {
		if a == "el RoleID actual apunta a un rol inexistente" {
			found = true
		}
	}
	if !found {
		t.Fatalf("debería reportar RoleID huérfano, anomalías=%v", row.Anomalies)
	}
}

// Un RoleID que apunta a un rol PERSONALIZADO válido (no uno de los 3 de sistema) no debe
// reportarse como huérfano — solo como conflicto (ya tiene RoleID asignado).
func TestDryRunUserRoleMigration_ValidCustomRoleIDNotReportedAsOrphan(t *testing.T) {
	db := setupMigrationDryRunTestDB(t)
	seedStandardSystemRoles(t, db)
	createDryRunUser(t, db, "sa@example.com", "superadmin", nil, true)
	custom := database.SARole{Name: "Personalizado"}
	db.Create(&custom)
	u := createDryRunUser(t, db, "admin@example.com", "admin", &custom.ID, true)

	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		t.Fatalf("DryRunUserRoleMigration: %v", err)
	}
	row := rowFor(t, report, u.ID)
	for _, a := range row.Anomalies {
		if a == "el RoleID actual apunta a un rol inexistente" {
			t.Fatalf("un RoleID a un rol personalizado VÁLIDO no debería reportarse como huérfano: %v", row.Anomalies)
		}
	}
}
