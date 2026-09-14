package service

// Fase 7 — Migración real de RoleID para usuarios reales (Grupo 7, RBAC central).
//
// MUY IMPORTANTE: este archivo implementa el MECANISMO. Nada de este archivo se invoca desde
// SeedCentral, desde el arranque de la aplicación, ni desde ninguna ruta HTTP existente — es
// deliberadamente código muerto en producción hasta que se conecte a un endpoint/CLI explícito en
// una fase posterior, y hasta entonces nadie puede dispararlo por accidente.
//
// Alcance único (Fase 7 §3): Role=="admin" && RoleID==nil → RoleID = ID del rol "Admin". Nada
// más. Role=="superadmin" nunca se toca (ni Role ni RoleID). Active/DeletedAt/Email/Name/Password
// nunca se tocan.
//
// Reutiliza DryRunUserRoleMigration (Fase 6) para el análisis fila-por-fila (anomalías,
// conflictos, resolución de "Admin" por nombre) — este archivo añade la capa de DECISIÓN
// (abortar vs. proceder), el mecanismo de exclusión mutua, el backup, la transacción real y el
// rollback.
import (
	"errors"
	"fmt"
	"sort"
	"time"

	"tukifac/pkg/database"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const userRoleMigrationLockName = "user_role_migration"

// Errores centinela de decisión (abortar toda la migración antes de escribir una sola fila).
var (
	ErrMigrationAlreadyRunning     = errors.New("ya hay una ejecución de la migración de RoleID en curso")
	ErrMigrationAdminRoleMissing   = errors.New("el rol \"Admin\" no existe (o no se resolvió de forma unívoca)")
	ErrMigrationAdminRoleDuplicate = errors.New("existe más de un rol llamado \"Admin\"")
	ErrMigrationNoOperationalSA    = errors.New("no existe ningún superadmin operativo")
	ErrMigrationUnknownRole        = errors.New("existen usuarios con Role desconocido o vacío")
	ErrMigrationRoleIDConflict     = errors.New("existen usuarios con RoleID inconsistente (conflicto u orfandad)")
	ErrMigrationAdminCatalogDrift  = errors.New("el rol \"Admin\" no tiene el catálogo de permisos esperado")
	ErrMigrationRowCountMismatch   = errors.New("la cantidad de filas afectadas no coincide con la planificada")

	ErrRollbackRunNotFound  = errors.New("no se encontró ningún backup para ese run_id")
	ErrRollbackStateChanged = errors.New("el estado actual de uno o más usuarios ya no coincide con lo que la migración dejó — abortado por seguridad")
)

// MigrationConflict describe una fila que impide continuar — se listan TODAS antes de abortar
// (nunca se detiene en la primera, para que el reporte sea completo de una sola pasada).
type MigrationConflict struct {
	UserID uint
	Email  string
	Reason string
}

// MigrationRunResult es el resultado de una ejecución (dry-run o real) de RunUserRoleMigration.
type MigrationRunResult struct {
	RunID  string
	DryRun bool

	Aborted     bool
	AbortReason string

	AdminRoleID          uint
	AdminRolePermissions []string

	ToMigrate       []uint // UserID de admins con RoleID==nil que recibirán el rol Admin
	AlreadyMigrated []uint // admins cuyo RoleID YA es el de Admin — no se tocan
	Conflicts       []MigrationConflict
	Warnings        []string // anomalías que NO abortan (p. ej. email duplicado)

	RowsPlanned  int
	RowsAffected int
	Executed     bool // true solo si de verdad se escribió algo (nunca en dry-run ni si Aborted)
}

// PlanUserRoleMigration hace SOLO lectura: corre DryRunUserRoleMigration y aplica las reglas de
// decisión de la Fase 7 (qué aborta, qué es "ya migrado", qué es conflicto). Puede llamarse tantas
// veces como se quiera, en cualquier momento, sin ningún efecto secundario ni necesidad de lock.
func PlanUserRoleMigration(db *gorm.DB) (*MigrationRunResult, error) {
	report, err := DryRunUserRoleMigration(db)
	if err != nil {
		return nil, err
	}

	result := &MigrationRunResult{AdminRolePermissions: report.AdminRolePermissions}

	// ---- Condiciones de aborto obligatorias (Fase 7 §2) ----
	for _, name := range report.MissingSystemRoles {
		if name == "Admin" {
			result.Aborted = true
			result.AbortReason = ErrMigrationAdminRoleMissing.Error()
			return result, nil
		}
	}
	for _, name := range report.DuplicateSystemRoles {
		if name == "Admin" {
			result.Aborted = true
			result.AbortReason = ErrMigrationAdminRoleDuplicate.Error()
			return result, nil
		}
	}
	if report.Blocked {
		result.Aborted = true
		result.AbortReason = ErrMigrationNoOperationalSA.Error() + ": " + report.BlockReason
		return result, nil
	}

	adminRoleID, ok := report.RoleIDsByName["Admin"]
	if !ok {
		// No debería llegar aquí (ya cubierto arriba), pero fail-closed de todas formas.
		result.Aborted = true
		result.AbortReason = ErrMigrationAdminRoleMissing.Error()
		return result, nil
	}
	result.AdminRoleID = adminRoleID

	if drift := adminCatalogDrift(report.AdminRolePermissions); drift != "" {
		result.Aborted = true
		result.AbortReason = ErrMigrationAdminCatalogDrift.Error() + ": " + drift
		return result, nil
	}

	var unknownRoleUsers []MigrationConflict
	var conflicts []MigrationConflict
	for _, row := range report.Rows {
		if row.Deleted {
			continue // excluido del universo de decisión: nunca migrado, nunca bloquea a otros
		}
		hasAnomaly := func(prefix string) bool {
			for _, a := range row.Anomalies {
				if len(a) >= len(prefix) && a[:len(prefix)] == prefix {
					return true
				}
			}
			return false
		}

		switch {
		case row.Role != "admin" && row.Role != "superadmin":
			unknownRoleUsers = append(unknownRoleUsers, MigrationConflict{
				UserID: row.UserID, Email: row.Email, Reason: fmt.Sprintf("Role desconocido/vacío: %q", row.Role),
			})
		case row.CurrentRoleID != nil && *row.CurrentRoleID == adminRoleID && row.Role == "admin":
			result.AlreadyMigrated = append(result.AlreadyMigrated, row.UserID)
		case row.CurrentRoleID != nil:
			reason := "ya tiene un RoleID distinto del rol Admin"
			if hasAnomaly("el RoleID actual apunta a un rol inexistente") {
				reason = "RoleID apunta a un rol inexistente (huérfano)"
			}
			conflicts = append(conflicts, MigrationConflict{UserID: row.UserID, Email: row.Email, Reason: reason})
		case row.Role == "admin" && row.CurrentRoleID == nil:
			result.ToMigrate = append(result.ToMigrate, row.UserID)
		case row.Role == "superadmin":
			// RoleID ya es nil (el único caso que llega aquí, dado el switch de arriba) — nada
			// que hacer, no se reporta como "ya migrado" (no hubo ninguna migración de por medio).
		}

		if hasAnomaly("email duplicado") {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("usuario %d (%s): email duplicado — no afecta el mapeo de RoleID, solo se reporta", row.UserID, row.Email))
		}
		if !row.Active && row.Role == "admin" && row.CurrentRoleID == nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("usuario %d (%s): inactivo — se migra igual (Active no se toca)", row.UserID, row.Email))
		}
	}

	if len(unknownRoleUsers) > 0 {
		result.Aborted = true
		result.Conflicts = unknownRoleUsers
		result.AbortReason = ErrMigrationUnknownRole.Error()
		return result, nil
	}
	if len(conflicts) > 0 {
		result.Aborted = true
		result.Conflicts = conflicts
		result.AbortReason = ErrMigrationRoleIDConflict.Error()
		return result, nil
	}

	result.RowsPlanned = len(result.ToMigrate)
	return result, nil
}

// adminCatalogDrift compara el catálogo ACTUAL del rol Admin contra database.SADefaultRoles
// (la definición aprobada del seed) — igualdad exacta de conjuntos. Retorna "" si coincide, o una
// descripción de la diferencia si no.
func adminCatalogDrift(current []string) string {
	var expected []string
	for _, def := range database.SADefaultRoles {
		if def.Name == "Admin" {
			expected = def.Permissions
			break
		}
	}
	currentSorted := append([]string(nil), current...)
	expectedSorted := append([]string(nil), expected...)
	sort.Strings(currentSorted)
	sort.Strings(expectedSorted)

	if len(currentSorted) == len(expectedSorted) {
		match := true
		for i := range currentSorted {
			if currentSorted[i] != expectedSorted[i] {
				match = false
				break
			}
		}
		if match {
			return ""
		}
	}

	missing := diffStrings(expectedSorted, currentSorted)
	extra := diffStrings(currentSorted, expectedSorted)
	return fmt.Sprintf("faltan=%v sobran=%v", missing, extra)
}

func diffStrings(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, s := range b {
		inB[s] = true
	}
	var out []string
	for _, s := range a {
		if !inB[s] {
			out = append(out, s)
		}
	}
	return out
}

// acquireMigrationLock inserta la fila de exclusión mutua — ver database.SAMigrationLock. Falla
// con ErrMigrationAlreadyRunning si ya existe (violación de PRIMARY KEY), sin importar el motor.
func acquireMigrationLock(db *gorm.DB, actor string) error {
	lock := database.SAMigrationLock{LockName: userRoleMigrationLockName, LockedAt: time.Now(), LockedBy: actor}
	if err := db.Create(&lock).Error; err != nil {
		return ErrMigrationAlreadyRunning
	}
	return nil
}

func releaseMigrationLock(db *gorm.DB) {
	db.Where("lock_name = ?", userRoleMigrationLockName).Delete(&database.SAMigrationLock{})
}

// RunUserRoleMigration ejecuta (o simula, si dryRun) la migración de RoleID. actor identifica
// quién/qué disparó la ejecución (para el lock y la auditoría) — nunca información sensible.
func RunUserRoleMigration(db *gorm.DB, actor string, dryRun bool) (*MigrationRunResult, error) {
	if !dryRun {
		if err := acquireMigrationLock(db, actor); err != nil {
			return &MigrationRunResult{Aborted: true, AbortReason: err.Error()}, nil
		}
		defer releaseMigrationLock(db)
	}

	result, err := PlanUserRoleMigration(db)
	if err != nil {
		return nil, err
	}
	result.RunID = uuid.NewString()
	result.DryRun = dryRun

	if dryRun || result.Aborted {
		// Dry-run: cero escrituras, incluida la auditoría (una ejecución que no cambia nada no es
		// un evento a registrar). Un aborto por validación si tiene su propia auditoría, más abajo.
		if result.Aborted && !dryRun {
			logUserRoleMigrationAudit(db, actor, result, false)
		}
		return result, nil
	}

	if len(result.ToMigrate) == 0 {
		// Idempotencia (Fase 7 §9): nada que hacer. Éxito, sin escrituras, sin auditoría (no
		// modificó nada — igual que un dry-run vacío no la necesita).
		result.Executed = true
		return result, nil
	}

	txErr := db.Transaction(func(tx *gorm.DB) error {
		var candidates []database.SuperAdminUser
		// Se relee DENTRO de la transacción, con el mismo filtro que decidió la selección — si
		// algo cambió entre el plan y aquí (p. ej. un ChangeUserRoleAPI concurrente le asignó otro
		// rol a uno de estos usuarios), simplemente deja de calificar y no se lo actualiza; el
		// conteo de filas afectadas al final detecta el desajuste y aborta TODO (ver más abajo).
		if err := tx.Where("id IN ? AND role = ? AND role_id IS NULL", result.ToMigrate, "admin").
			Find(&candidates).Error; err != nil {
			return err
		}

		for _, u := range candidates {
			backup := database.SAUserRoleMigrationBackup{
				RunID: result.RunID, UserID: u.ID,
				RoleBefore: u.Role, RoleIDBefore: u.RoleID,
				RoleAfter: u.Role, RoleIDAfter: &result.AdminRoleID,
				ActiveBefore: u.Active,
			}
			if u.DeletedAt.Valid {
				t := u.DeletedAt.Time
				backup.DeletedAtBefore = &t
			}
			if err := tx.Create(&backup).Error; err != nil {
				return err
			}
		}

		affected := 0
		for _, u := range candidates {
			res := tx.Model(&database.SuperAdminUser{}).
				Where("id = ? AND role = ? AND role_id IS NULL", u.ID, "admin").
				Update("role_id", result.AdminRoleID)
			if res.Error != nil {
				return res.Error
			}
			affected += int(res.RowsAffected)
			if res.RowsAffected != 1 {
				continue
			}
			if err := u.IncrementTokenVersion(tx); err != nil {
				return err
			}
		}

		if affected != len(result.ToMigrate) {
			return fmt.Errorf("%w: planificadas %d, afectadas %d", ErrMigrationRowCountMismatch, len(result.ToMigrate), affected)
		}
		result.RowsAffected = affected
		return nil
	})

	if txErr != nil {
		result.Aborted = true
		result.AbortReason = txErr.Error()
		logUserRoleMigrationAudit(db, actor, result, false)
		return result, nil
	}

	result.Executed = true
	logUserRoleMigrationAudit(db, actor, result, true)
	return result, nil
}

func logUserRoleMigrationAudit(db *gorm.DB, actor string, result *MigrationRunResult, success bool) {
	if db == nil {
		return
	}
	database.WriteAuditLogTx(db, &database.AuditLog{
		Action: "user_role_migration",
		Entity: "sa_user_role_migration_run",
		Payload: fmt.Sprintf(
			`{"run_id":%q,"actor":%q,"success":%v,"aborted":%v,"abort_reason":%q,"rows_planned":%d,"rows_affected":%d,"user_ids":%v}`,
			result.RunID, actor, success, result.Aborted, result.AbortReason, result.RowsPlanned, result.RowsAffected, result.ToMigrate,
		),
	})
}

// ==================== Rollback (Fase 7 §11) ====================

// RollbackResult es el resultado de RollbackUserRoleMigration.
type RollbackResult struct {
	RunID     string
	Aborted   bool
	Reason    string
	Restored  []uint
	RowsFound int
}

// RollbackUserRoleMigration restaura RoleID a su valor anterior para TODOS los usuarios que la
// ejecución `runID` modificó — SOLO si el estado actual de cada uno todavía coincide exactamente
// con lo que esa ejecución dejó (Role/RoleID). Si algo más lo cambió después (p. ej. un operador
// reasignó el rol a mano), el rollback se detiene por completo y reporta el conflicto — nunca
// pisa un cambio posterior sin avisar.
func RollbackUserRoleMigration(db *gorm.DB, runID, actor string) (*RollbackResult, error) {
	if err := acquireMigrationLock(db, actor); err != nil {
		return &RollbackResult{RunID: runID, Aborted: true, Reason: err.Error()}, nil
	}
	defer releaseMigrationLock(db)

	var backups []database.SAUserRoleMigrationBackup
	if err := db.Where("run_id = ?", runID).Find(&backups).Error; err != nil {
		return nil, err
	}
	if len(backups) == 0 {
		return &RollbackResult{RunID: runID, Aborted: true, Reason: ErrRollbackRunNotFound.Error()}, nil
	}

	result := &RollbackResult{RunID: runID, RowsFound: len(backups)}

	// Verificación de conflicto: TODAS las filas primero, antes de tocar ninguna.
	var conflicts []string
	for _, b := range backups {
		var current database.SuperAdminUser
		if err := db.First(&current, b.UserID).Error; err != nil {
			conflicts = append(conflicts, fmt.Sprintf("usuario %d: %v", b.UserID, err))
			continue
		}
		if current.Role != b.RoleAfter || !roleIDEqual(current.RoleID, b.RoleIDAfter) {
			conflicts = append(conflicts, fmt.Sprintf(
				"usuario %d: estado actual (role=%s, role_id=%v) ya no coincide con lo que dejó la migración (role=%s, role_id=%v)",
				b.UserID, current.Role, current.RoleID, b.RoleAfter, b.RoleIDAfter))
		}
	}
	if len(conflicts) > 0 {
		result.Aborted = true
		result.Reason = ErrRollbackStateChanged.Error() + ": " + fmt.Sprint(conflicts)
		return result, nil
	}

	txErr := db.Transaction(func(tx *gorm.DB) error {
		affected := 0
		for _, b := range backups {
			var user database.SuperAdminUser
			if err := tx.First(&user, b.UserID).Error; err != nil {
				return err
			}
			res := tx.Model(&database.SuperAdminUser{}).
				Where("id = ? AND role = ? AND role_id IS NOT NULL AND role_id = ?", b.UserID, b.RoleAfter, *b.RoleIDAfter).
				Update("role_id", b.RoleIDBefore)
			if res.Error != nil {
				return res.Error
			}
			affected += int(res.RowsAffected)
			if res.RowsAffected != 1 {
				continue
			}
			if err := user.IncrementTokenVersion(tx); err != nil {
				return err
			}
			result.Restored = append(result.Restored, b.UserID)
		}
		if affected != len(backups) {
			return fmt.Errorf("%w: esperadas %d, restauradas %d", ErrMigrationRowCountMismatch, len(backups), affected)
		}
		return nil
	})
	if txErr != nil {
		result.Aborted = true
		result.Reason = txErr.Error()
		result.Restored = nil
		logUserRoleMigrationRollbackAudit(db, actor, result, false)
		return result, nil
	}

	logUserRoleMigrationRollbackAudit(db, actor, result, true)
	return result, nil
}

func logUserRoleMigrationRollbackAudit(db *gorm.DB, actor string, result *RollbackResult, success bool) {
	if db == nil {
		return
	}
	database.WriteAuditLogTx(db, &database.AuditLog{
		Action: "user_role_migration_rollback",
		Entity: "sa_user_role_migration_run",
		Payload: fmt.Sprintf(`{"run_id":%q,"actor":%q,"success":%v,"aborted":%v,"reason":%q,"restored":%v}`,
			result.RunID, actor, success, result.Aborted, result.Reason, result.Restored),
	})
}
