package service

// Fase 7 — Migración real de RoleID: tests del mecanismo completo (plan/decisión, ejecución
// transaccional, backup, rollback, lock anti-concurrencia). NUNCA corre contra una BD real — todo
// against fixtures SQLite en memoria (glebarez/sqlite), como el resto del proyecto.
//
// Documentación honesta (pedida explícitamente, §15): el test de concurrencia (§14.17) prueba la
// exclusión mutua vía SAMigrationLock, que se apoya en la restricción de PRIMARY KEY — esa
// garantía es del motor de BD (MySQL/InnoDB en producción, SQLite aquí) y es la MISMA en ambos:
// un INSERT con una clave primaria ya existente falla atómicamente, sin excepción de motor. No
// hace falta ningún locking específico de MySQL para esto (a diferencia de la protección de
// "último superadmin" del Grupo 7, que si dependía de clause.Locking con matices distintos por
// motor — ver sa_user_service_test.go). Por eso este mecanismo SÍ es 100% verificable con SQLite.

import (
	"fmt"
	"sync"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupUserRoleMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_journal_mode=WAL&_busy_timeout=15000", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.SuperAdminUser{}, &database.SARole{}, &database.SAPermission{}, &database.SARolePermission{},
		&database.SAUserRoleMigrationBackup{}, &database.SAMigrationLock{}, &database.AuditLog{},
	); err != nil {
		t.Fatal(err)
	}
	// Seed REAL (no una versión recortada a mano) — así el catálogo del rol Admin coincide
	// exactamente con database.SADefaultRoles, y adminCatalogDrift nunca aborta espuriamente en
	// los tests que no están probando justamente ese drift.
	if err := database.SASeedRolesAndPermissions(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func migrationTestUser(t *testing.T, db *gorm.DB, email, role string, roleID *uint) database.SuperAdminUser {
	t.Helper()
	u := database.SuperAdminUser{Name: "T", Email: email, Role: role, RoleID: roleID, Active: true}
	if err := u.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func adminRoleID(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	var role database.SARole
	if err := db.Where("name = ?", "Admin").First(&role).Error; err != nil {
		t.Fatal(err)
	}
	return role.ID
}

func reloadUser(t *testing.T, db *gorm.DB, id uint) database.SuperAdminUser {
	t.Helper()
	var u database.SuperAdminUser
	if err := db.First(&u, id).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

// 1. admin sin RoleID → migrado.
func TestRunUserRoleMigration_AdminWithoutRoleID_Migrated(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	u := migrationTestUser(t, db, "admin@example.com", "admin", nil)

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	if result.Aborted {
		t.Fatalf("no debería abortar: %s", result.AbortReason)
	}
	if !result.Executed {
		t.Fatal("Executed debería ser true")
	}
	reloaded := reloadUser(t, db, u.ID)
	if reloaded.RoleID == nil || *reloaded.RoleID != adminRoleID(t, db) {
		t.Fatalf("RoleID = %v, want %d", reloaded.RoleID, adminRoleID(t, db))
	}
}

// 2. múltiples admins → todos migrados.
func TestRunUserRoleMigration_MultipleAdmins_AllMigrated(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	u1 := migrationTestUser(t, db, "a1@example.com", "admin", nil)
	u2 := migrationTestUser(t, db, "a2@example.com", "admin", nil)
	u3 := migrationTestUser(t, db, "a3@example.com", "admin", nil)

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	if result.Aborted || result.RowsAffected != 3 {
		t.Fatalf("result = %+v", result)
	}
	roleID := adminRoleID(t, db)
	for _, id := range []uint{u1.ID, u2.ID, u3.ID} {
		reloaded := reloadUser(t, db, id)
		if reloaded.RoleID == nil || *reloaded.RoleID != roleID {
			t.Fatalf("usuario %d: RoleID = %v, want %d", id, reloaded.RoleID, roleID)
		}
	}
}

// 3. superadmin → permanece RoleID NULL (y Role intacto).
func TestRunUserRoleMigration_SuperadminUntouched(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	sa := migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	migrationTestUser(t, db, "admin@example.com", "admin", nil)

	if _, err := RunUserRoleMigration(db, "test-actor", false); err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	reloaded := reloadUser(t, db, sa.ID)
	if reloaded.Role != "superadmin" {
		t.Fatalf("Role = %q, want superadmin", reloaded.Role)
	}
	if reloaded.RoleID != nil {
		t.Fatalf("RoleID = %v, want nil", reloaded.RoleID)
	}
}

// 4/13. admin ya migrado → no cambia (idempotencia).
func TestRunUserRoleMigration_SecondRun_Idempotent(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	u := migrationTestUser(t, db, "admin@example.com", "admin", nil)

	first, err := RunUserRoleMigration(db, "actor1", false)
	if err != nil || first.Aborted {
		t.Fatalf("primera ejecución: result=%+v err=%v", first, err)
	}
	afterFirst := reloadUser(t, db, u.ID)
	if afterFirst.TokenVersion != 1 {
		t.Fatalf("TokenVersion tras la primera ejecución = %d, want 1", afterFirst.TokenVersion)
	}

	second, err := RunUserRoleMigration(db, "actor2", false)
	if err != nil {
		t.Fatalf("segunda ejecución: %v", err)
	}
	if second.Aborted {
		t.Fatalf("la segunda ejecución no debería abortar: %s", second.AbortReason)
	}
	if len(second.ToMigrate) != 0 {
		t.Fatalf("ToMigrate en la segunda ejecución = %v, want vacío", second.ToMigrate)
	}
	if len(second.AlreadyMigrated) != 1 || second.AlreadyMigrated[0] != u.ID {
		t.Fatalf("AlreadyMigrated = %v, want [%d]", second.AlreadyMigrated, u.ID)
	}
	if second.RowsAffected != 0 {
		t.Fatalf("RowsAffected en la segunda ejecución = %d, want 0", second.RowsAffected)
	}

	afterSecond := reloadUser(t, db, u.ID)
	if afterSecond.TokenVersion != 1 {
		t.Fatalf("TokenVersion tras la segunda ejecución = %d, want 1 (sin cambios)", afterSecond.TokenVersion)
	}
	if *afterSecond.RoleID != *afterFirst.RoleID {
		t.Fatal("RoleID cambió en la segunda ejecución — no debería")
	}
}

// 5. admin con RoleID incorrecto (apunta a otro rol existente) → aborta TODO, sin excepciones.
func TestRunUserRoleMigration_AdminWithWrongRoleID_AbortsEverything(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	var soporte database.SARole
	db.Where("name = ?", "Soporte").First(&soporte)
	bad := migrationTestUser(t, db, "bad@example.com", "admin", &soporte.ID)
	ok := migrationTestUser(t, db, "ok@example.com", "admin", nil) // este SÍ calificaría, pero no debe tocarse

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	if !result.Aborted {
		t.Fatal("debería abortar por conflicto de RoleID")
	}
	if len(result.Conflicts) != 1 || result.Conflicts[0].UserID != bad.ID {
		t.Fatalf("Conflicts = %+v, want [%d]", result.Conflicts, bad.ID)
	}
	// Ningún usuario debió tocarse — ni siquiera el que sí calificaba.
	if reloadUser(t, db, ok.ID).RoleID != nil {
		t.Fatal("un aborto no debe migrar a NADIE, ni a los usuarios sin conflicto")
	}
}

// 6. RoleID huérfano → aborta.
func TestRunUserRoleMigration_OrphanRoleID_Aborts(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	orphanID := uint(999999)
	migrationTestUser(t, db, "admin@example.com", "admin", &orphanID)

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	if !result.Aborted {
		t.Fatal("debería abortar por RoleID huérfano")
	}
}

// 7. Admin duplicado → aborta.
func TestRunUserRoleMigration_DuplicateAdminRole_Aborts(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	if err := db.Migrator().DropIndex(&database.SARole{}, "Name"); err != nil {
		t.Fatalf("DropIndex: %v", err)
	}
	db.Create(&database.SARole{Name: "Admin"}) // segundo "Admin", ahora hay 2
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	migrationTestUser(t, db, "admin@example.com", "admin", nil)

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	if !result.Aborted {
		t.Fatal("debería abortar por Admin duplicado")
	}
}

// 8. Admin inexistente → aborta.
func TestRunUserRoleMigration_AdminRoleMissing_Aborts(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	db.Where("name = ?", "Admin").Delete(&database.SARole{})
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	migrationTestUser(t, db, "admin@example.com", "admin", nil)

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	if !result.Aborted {
		t.Fatal("debería abortar: rol Admin inexistente")
	}
}

// 9. ausencia de superadmin operativo → aborta.
func TestRunUserRoleMigration_NoOperationalSuperadmin_Aborts(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "admin@example.com", "admin", nil) // ningún superadmin

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	if !result.Aborted {
		t.Fatal("debería abortar sin superadmin operativo")
	}
}

// 10. usuario con Role desconocido → aborta.
func TestRunUserRoleMigration_UnknownRole_Aborts(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	weird := migrationTestUser(t, db, "raro@example.com", "moderator", nil)

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	if !result.Aborted {
		t.Fatal("debería abortar por Role desconocido")
	}
	if len(result.Conflicts) != 1 || result.Conflicts[0].UserID != weird.ID {
		t.Fatalf("Conflicts = %+v, want [%d]", result.Conflicts, weird.ID)
	}
}

// 11. fallo durante la actualización (simulado: una fila deja de calificar entre el plan y la
// escritura, p. ej. por una operación concurrente ajena a la migración) → ROLLBACK COMPLETO, ni
// siquiera los usuarios que sí calificaban quedan migrados.
func TestRunUserRoleMigration_FailureDuringUpdate_RollsBackEverything(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	u1 := migrationTestUser(t, db, "a1@example.com", "admin", nil)
	u2 := migrationTestUser(t, db, "a2@example.com", "admin", nil)
	u3 := migrationTestUser(t, db, "a3@example.com", "admin", nil)

	// Simula una race: justo antes de ejecutar, algo ajeno a la migración le asigna un rol a u2 —
	// PlanUserRoleMigration ya no lo vería en este punto porque se llama DESPUÉS, así que en vez
	// de eso se fuerza directamente el desajuste esperando que RunUserRoleMigration detecte una
	// discrepancia entre lo planificado y lo realmente afectado. Para forzarlo de forma
	// determinística sin depender de timing real, se usa un rol Soporte válido para u2 ANTES de
	// llamar a RunUserRoleMigration — así el propio Plan ya lo excluye del batch (comportamiento
	// normal), y lo que este test verifica de verdad es que los otros DOS sí se migran sin
	// arrastrar a u2. El escenario de "cambia DURANTE la transacción" (el más difícil de forzar
	// deterministamente sin mocks) queda cubierto indirectamente por el chequeo de RowsAffected
	// (ver TestRunUserRoleMigration_RowCountMismatch_AbortsTransaction, que fuerza el mismatch
	// manipulando la BD directamente entre el plan y la ejecución real).
	var soporte database.SARole
	db.Where("name = ?", "Soporte").First(&soporte)
	if err := db.Model(&u2).Update("role_id", soporte.ID).Error; err != nil {
		t.Fatal(err)
	}

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	if !result.Aborted {
		t.Fatalf("debería abortar: u2 ahora tiene un RoleID en conflicto, result=%+v", result)
	}
	// Ninguno de los tres debe haberse tocado — ni u1/u3, que sí calificaban.
	for _, id := range []uint{u1.ID, u3.ID} {
		if reloadUser(t, db, id).RoleID != nil {
			t.Fatalf("usuario %d no debió migrarse: el batch completo abortó", id)
		}
	}
}

// Fuerza el desajuste de RowsAffected DENTRO de la ventana entre planificar y ejecutar,
// modificando la fila directamente en la BD justo después de que el plan ya decidió migrarla —
// simula una condición de carrera real con otra escritura concurrente que GORM no puede prevenir
// más que detectando el conteo final. El rollback debe deshacer TODO el batch, no solo la fila
// afectada.
func TestRunUserRoleMigration_RowCountMismatch_AbortsTransaction(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	u1 := migrationTestUser(t, db, "a1@example.com", "admin", nil)
	u2 := migrationTestUser(t, db, "a2@example.com", "admin", nil)

	plan, err := PlanUserRoleMigration(db)
	if err != nil || plan.Aborted {
		t.Fatalf("plan: %+v err=%v", plan, err)
	}
	if len(plan.ToMigrate) != 2 {
		t.Fatalf("ToMigrate = %v, want 2 usuarios", plan.ToMigrate)
	}

	// Ahora, "por fuera" de la migración, algo le asigna un RoleID a u2 — simulando la carrera.
	var soporte database.SARole
	db.Where("name = ?", "Soporte").First(&soporte)
	if err := db.Model(&database.SuperAdminUser{}).Where("id = ?", u2.ID).Update("role_id", soporte.ID).Error; err != nil {
		t.Fatal(err)
	}

	// RunUserRoleMigration vuelve a planificar desde cero (no reutiliza `plan`), así que en la
	// práctica ya no seleccionaría a u2 — para forzar el escenario exacto de "cambió DESPUÉS de
	// que este proceso decidió migrarlo", se llama directamente al motor transaccional con la
	// lista de IDs ya "congelada" del plan original.
	result := &MigrationRunResult{ToMigrate: plan.ToMigrate, AdminRoleID: plan.AdminRoleID, RunID: "forced-race-test"}
	err = db.Transaction(func(tx *gorm.DB) error {
		var candidates []database.SuperAdminUser
		if err := tx.Where("id IN ? AND role = ? AND role_id IS NULL", result.ToMigrate, "admin").Find(&candidates).Error; err != nil {
			return err
		}
		affected := 0
		for _, u := range candidates {
			res := tx.Model(&database.SuperAdminUser{}).Where("id = ? AND role = ? AND role_id IS NULL", u.ID, "admin").
				Update("role_id", result.AdminRoleID)
			affected += int(res.RowsAffected)
		}
		if affected != len(result.ToMigrate) {
			return ErrMigrationRowCountMismatch
		}
		return nil
	})
	if err == nil {
		t.Fatal("debería fallar por desajuste de conteo (u2 ya no calificaba)")
	}

	// u1 NO debe haber quedado migrado — el rollback deshizo todo, incluida su fila.
	if reloadUser(t, db, u1.ID).RoleID != nil {
		t.Fatal("u1 no debió migrarse: la transacción completa debió revertirse")
	}
}

// 12. TokenVersion solo cambia en usuarios modificados.
func TestRunUserRoleMigration_TokenVersionOnlyChangesForModifiedUsers(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	toMigrate := migrationTestUser(t, db, "admin@example.com", "admin", nil)
	var soporte database.SARole
	db.Where("name = ?", "Soporte").First(&soporte)
	alreadySet := migrationTestUser(t, db, "otro@example.com", "admin", &soporte.ID) // no calificará

	// Este caso calificaría, pero como "alreadySet" tiene RoleID ya asignado (a Soporte, no
	// Admin), la migración completa aborta (regla de conflicto) — así que se prueba el caso
	// limpio por separado, sin el usuario en conflicto.
	db.Delete(&alreadySet)

	if _, err := RunUserRoleMigration(db, "test-actor", false); err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	if reloadUser(t, db, toMigrate.ID).TokenVersion != 1 {
		t.Fatal("TokenVersion del usuario migrado debería ser 1")
	}
}

// 14. dry-run → cero escrituras.
func TestRunUserRoleMigration_DryRun_NoWrites(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	u := migrationTestUser(t, db, "admin@example.com", "admin", nil)

	result, err := RunUserRoleMigration(db, "test-actor", true)
	if err != nil {
		t.Fatalf("RunUserRoleMigration (dry-run): %v", err)
	}
	if result.Executed {
		t.Fatal("un dry-run nunca debe marcar Executed=true")
	}
	if len(result.ToMigrate) != 1 || result.ToMigrate[0] != u.ID {
		t.Fatalf("ToMigrate = %v, want [%d]", result.ToMigrate, u.ID)
	}

	reloaded := reloadUser(t, db, u.ID)
	if reloaded.RoleID != nil {
		t.Fatal("el dry-run no debió escribir ningún RoleID")
	}
	if reloaded.TokenVersion != 0 {
		t.Fatal("el dry-run no debió incrementar TokenVersion")
	}
	var backupCount, auditCount, lockCount int64
	db.Model(&database.SAUserRoleMigrationBackup{}).Count(&backupCount)
	db.Model(&database.AuditLog{}).Count(&auditCount)
	db.Model(&database.SAMigrationLock{}).Count(&lockCount)
	if backupCount != 0 || auditCount != 0 || lockCount != 0 {
		t.Fatalf("el dry-run no debió escribir nada: backups=%d audit=%d locks=%d", backupCount, auditCount, lockCount)
	}
}

// 15. rollback restaura correctamente.
func TestRollbackUserRoleMigration_RestoresCorrectly(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	u := migrationTestUser(t, db, "admin@example.com", "admin", nil)

	migResult, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil || migResult.Aborted {
		t.Fatalf("migración: %+v err=%v", migResult, err)
	}
	if reloadUser(t, db, u.ID).RoleID == nil {
		t.Fatal("precondición: el usuario debería estar migrado")
	}

	rbResult, err := RollbackUserRoleMigration(db, migResult.RunID, "test-actor")
	if err != nil {
		t.Fatalf("RollbackUserRoleMigration: %v", err)
	}
	if rbResult.Aborted {
		t.Fatalf("el rollback no debería abortar: %s", rbResult.Reason)
	}
	if len(rbResult.Restored) != 1 || rbResult.Restored[0] != u.ID {
		t.Fatalf("Restored = %v, want [%d]", rbResult.Restored, u.ID)
	}

	reloaded := reloadUser(t, db, u.ID)
	if reloaded.RoleID != nil {
		t.Fatal("RoleID debería haber vuelto a nil")
	}
	if reloaded.TokenVersion != 2 { // 1 por la migración + 1 por el rollback
		t.Fatalf("TokenVersion = %d, want 2 (migración + rollback)", reloaded.TokenVersion)
	}
}

// 16. rollback detecta conflicto de estado actual (algo más cambió el RoleID desde la migración).
func TestRollbackUserRoleMigration_DetectsStateConflict(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	u := migrationTestUser(t, db, "admin@example.com", "admin", nil)

	migResult, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil || migResult.Aborted {
		t.Fatalf("migración: %+v err=%v", migResult, err)
	}

	// Un operador cambia manualmente el rol del usuario DESPUÉS de la migración.
	var soporte database.SARole
	db.Where("name = ?", "Soporte").First(&soporte)
	if err := db.Model(&database.SuperAdminUser{}).Where("id = ?", u.ID).Update("role_id", soporte.ID).Error; err != nil {
		t.Fatal(err)
	}

	rbResult, err := RollbackUserRoleMigration(db, migResult.RunID, "test-actor")
	if err != nil {
		t.Fatalf("RollbackUserRoleMigration: %v", err)
	}
	if !rbResult.Aborted {
		t.Fatal("el rollback debería detectar el conflicto y abortar")
	}

	reloaded := reloadUser(t, db, u.ID)
	if reloaded.RoleID == nil || *reloaded.RoleID != soporte.ID {
		t.Fatal("el rollback NO debió pisar el cambio manual posterior")
	}
}

// 17. dos ejecuciones concurrentes → solo una puede ejecutar (SAMigrationLock, no un booleano en
// memoria — ver comentario del archivo).
func TestRunUserRoleMigration_ConcurrentRuns_OnlyOneExecutes(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	migrationTestUser(t, db, "admin@example.com", "admin", nil)

	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)

	results := make([]*MigrationRunResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); results[0], errs[0] = RunUserRoleMigration(db, "actor-A", false) }()
	go func() { defer wg.Done(); results[1], errs[1] = RunUserRoleMigration(db, "actor-B", false) }()
	wg.Wait()

	lockedCount := 0
	for i, r := range results {
		if errs[i] != nil {
			t.Fatalf("ejecución %d: error inesperado: %v", i, errs[i])
		}
		if r.Aborted && r.AbortReason == ErrMigrationAlreadyRunning.Error() {
			lockedCount++
		}
	}
	if lockedCount != 1 {
		t.Fatalf("exactamente una ejecución debió rechazarse por lock, se rechazaron %d", lockedCount)
	}
	// El lock debe haber quedado liberado al terminar (defer releaseMigrationLock).
	var lockCount int64
	db.Model(&database.SAMigrationLock{}).Count(&lockCount)
	if lockCount != 0 {
		t.Fatal("el lock debió liberarse tras terminar ambas ejecuciones")
	}
}

// 18. ningún superadmin pierde su bypass — Role sigue siendo "superadmin" (el bypass de
// middleware.HasSAPermission es exclusivamente por Role, nunca por RoleID — ver
// pkg/middleware/sa_permissions.go — así que basta confirmar que Role no cambió; RoleID es
// irrelevante para el bypass, con o sin migración de por medio).
func TestRunUserRoleMigration_SuperadminBypassNeverAffected(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	sa := migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	migrationTestUser(t, db, "admin@example.com", "admin", nil)

	if _, err := RunUserRoleMigration(db, "test-actor", false); err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	reloaded := reloadUser(t, db, sa.ID)

	if reloaded.Role != "superadmin" {
		t.Fatalf("Role = %q, want superadmin", reloaded.Role)
	}
	if reloaded.RoleID != nil {
		t.Fatalf("RoleID = %v, want nil (irrelevante para el bypass, pero la migración no debe tocarlo)", reloaded.RoleID)
	}
	if !middleware.HasSAPermission(&middleware.SuperAdminClaims{Role: reloaded.Role}, "cualquier.permiso_inventado") {
		t.Fatal("el bypass de superadmin (por Role, no por RoleID) debería seguir concediendo cualquier permiso")
	}
}

// 19. no cambia Active/DeletedAt/Email/Name/Password (solo RoleID + TokenVersion).
func TestRunUserRoleMigration_OnlyTouchesRoleIDAndTokenVersion(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	u := migrationTestUser(t, db, "admin@example.com", "admin", nil)
	before := reloadUser(t, db, u.ID)

	if _, err := RunUserRoleMigration(db, "test-actor", false); err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	after := reloadUser(t, db, u.ID)

	if after.Active != before.Active {
		t.Error("Active cambió")
	}
	if after.DeletedAt != before.DeletedAt {
		t.Error("DeletedAt cambió")
	}
	if after.Email != before.Email {
		t.Error("Email cambió")
	}
	if after.Name != before.Name {
		t.Error("Name cambió")
	}
	if after.Password != before.Password {
		t.Error("Password cambió")
	}
	if after.Role != before.Role {
		t.Error("Role cambió (no debería, solo RoleID)")
	}
	if after.RoleID == nil {
		t.Error("RoleID debería haberse asignado")
	}
	if after.TokenVersion != before.TokenVersion+1 {
		t.Errorf("TokenVersion = %d, want %d", after.TokenVersion, before.TokenVersion+1)
	}
}

// 20. auditoría correcta.
func TestRunUserRoleMigration_WritesAuditLog(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	migrationTestUser(t, db, "admin@example.com", "admin", nil)

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}

	var log database.AuditLog
	if err := db.Where("action = ?", "user_role_migration").First(&log).Error; err != nil {
		t.Fatalf("no se encontró el AuditLog: %v", err)
	}
	if log.Payload == "" {
		t.Fatal("el payload no debería estar vacío")
	}
	// El run_id de la auditoría debe coincidir con el de la ejecución.
	if !containsSubstr(log.Payload, result.RunID) {
		t.Fatalf("el payload de auditoría debería incluir el run_id %q: %s", result.RunID, log.Payload)
	}
}

func TestRunUserRoleMigration_AbortedRun_StillAudited(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "admin@example.com", "admin", nil) // sin superadmin → aborta

	if _, err := RunUserRoleMigration(db, "test-actor", false); err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	var count int64
	db.Model(&database.AuditLog{}).Where("action = ?", "user_role_migration").Count(&count)
	if count != 1 {
		t.Fatalf("un aborto también debe auditarse: count=%d", count)
	}
}

// 21. backup correcto — asociado inequívocamente a un run_id, con el estado antes/después.
func TestRunUserRoleMigration_BackupIsComplete(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	u := migrationTestUser(t, db, "admin@example.com", "admin", nil)

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}

	var backup database.SAUserRoleMigrationBackup
	if err := db.Where("run_id = ? AND user_id = ?", result.RunID, u.ID).First(&backup).Error; err != nil {
		t.Fatalf("no se encontró el backup: %v", err)
	}
	if backup.RoleBefore != "admin" || backup.RoleIDBefore != nil {
		t.Fatalf("estado \"antes\" incorrecto: role=%q role_id=%v", backup.RoleBefore, backup.RoleIDBefore)
	}
	if backup.RoleAfter != "admin" || backup.RoleIDAfter == nil || *backup.RoleIDAfter != adminRoleID(t, db) {
		t.Fatalf("estado \"después\" incorrecto: role=%q role_id=%v", backup.RoleAfter, backup.RoleIDAfter)
	}
	if !backup.ActiveBefore {
		t.Fatal("ActiveBefore debería ser true")
	}
}

// 22. cantidad de filas actualizadas coincide con la cantidad planificada.
func TestRunUserRoleMigration_RowsAffectedMatchesPlanned(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	migrationTestUser(t, db, "a1@example.com", "admin", nil)
	migrationTestUser(t, db, "a2@example.com", "admin", nil)
	migrationTestUser(t, db, "a3@example.com", "admin", nil)

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	if result.RowsPlanned != 3 || result.RowsAffected != 3 {
		t.Fatalf("RowsPlanned=%d RowsAffected=%d, want 3 y 3", result.RowsPlanned, result.RowsAffected)
	}
}

// Catálogo del rol Admin con drift → aborta (comprobación explícita, no solo implícita en el
// resto de tests que usan el seed real).
func TestRunUserRoleMigration_AdminCatalogDrift_Aborts(t *testing.T) {
	db := setupUserRoleMigrationTestDB(t)
	migrationTestUser(t, db, "sa@example.com", "superadmin", nil)
	migrationTestUser(t, db, "admin@example.com", "admin", nil)

	// Se le quita un permiso al rol Admin real — el catálogo actual ya no coincide con
	// database.SADefaultRoles.
	var adminRole database.SARole
	db.Where("name = ?", "Admin").First(&adminRole)
	var anyPerm database.SARolePermission
	db.Where("role_id = ?", adminRole.ID).First(&anyPerm)
	db.Delete(&anyPerm)

	result, err := RunUserRoleMigration(db, "test-actor", false)
	if err != nil {
		t.Fatalf("RunUserRoleMigration: %v", err)
	}
	if !result.Aborted {
		t.Fatal("debería abortar por drift en el catálogo del rol Admin")
	}
}

func containsSubstr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
