package service

// Fase 6 — Pre-migración de usuarios reales (RBAC central, Grupo 7).
//
// DryRunUserRoleMigration es una operación de SOLO LECTURA: nunca escribe en SuperAdminUser ni en
// ninguna otra tabla. Produce un plan (una fila por usuario, incluidos los eliminados vía
// Unscoped) con el RoleID propuesto y el motivo, más un resumen de superadmins/anomalías. NO
// ejecuta ningún UPDATE — la ejecución real de la migración es un paso separado, posterior y
// explícitamente aprobado (ver instrucción del usuario, Fase 6 §13).
//
// Mapeo propuesto (aprobado en Fase 6 §3):
//   - Role == "admin"      → RoleID propuesto = ID del rol "Admin" (resuelto por NOMBRE, nunca
//     hardcodeado — ver resolveSystemRoleByName).
//   - Role == "superadmin" → RoleID propuesto = nil (el bypass de Role sigue siendo total e
//     independiente de RoleID; no se le asigna un rol granular salvo razón técnica explícita, que
//     esta fase no encontró ninguna).
//   - Cualquier otra combinación (RoleID ya asignado, Role desconocido/vacío, usuario eliminado,
//     rol de sistema ausente/duplicado) → SIN propuesta, reportada como anomalía o conflicto para
//     que un humano decida antes de la migración real.
import (
	"gorm.io/gorm"

	"tukifac/pkg/database"
)

// systemRoleNames son los tres roles de sistema que la migración necesita resolver por nombre
// (nunca por ID) — ver database.SADefaultRoles, la fuente de verdad del seed.
var systemRoleNames = []string{"Admin", "Soporte", "Finanzas"}

// MigrationPlanRow es una fila del plan de migración — una por SuperAdminUser existente,
// incluidos los soft-deleted (para que también queden reportados, nunca migrados).
type MigrationPlanRow struct {
	UserID           uint
	Email            string
	Role             string
	CurrentRoleID    *uint
	Active           bool
	Deleted          bool
	ProposedRoleID   *uint
	ProposedRoleName string
	Reason           string
	Anomalies        []string
}

// HasProposal indica si esta fila tiene un cambio propuesto (RoleID nuevo distinto del actual) —
// falso para superadmins que ya tienen RoleID=nil (no hay nada que cambiar) y para cualquier fila
// con anomalías.
func (r MigrationPlanRow) HasProposal() bool {
	return len(r.Anomalies) == 0 && !roleIDEqual(r.CurrentRoleID, r.ProposedRoleID)
}

func roleIDEqual(a, b *uint) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// SuperadminInventory — conteo de superadmins por estado, ver Fase 6 §6.
type SuperadminInventory struct {
	Total       int // todas las filas Role=="superadmin", incluidas eliminadas (Unscoped)
	Active      int // Role=="superadmin" && Active==true && no eliminado — "operativos"
	Inactive    int // Role=="superadmin" && Active==false && no eliminado
	SoftDeleted int // Role=="superadmin" && eliminado (soft-delete)
}

// MigrationDryRunReport es el resultado completo del dry-run — ver Fase 6 §14.
type MigrationDryRunReport struct {
	Rows []MigrationPlanRow

	Superadmins SuperadminInventory

	// RoleIDsByName: nombre de rol de sistema → ID, solo para los resueltos sin ambigüedad.
	RoleIDsByName map[string]uint
	// MissingSystemRoles: nombres de systemRoleNames que no se encontraron en sa_roles.
	MissingSystemRoles []string
	// DuplicateSystemRoles: nombres que aparecieron más de una vez en sa_roles (no debería
	// suceder por el uniqueIndex, pero se verifica en código, no se asume el esquema).
	DuplicateSystemRoles []string

	// AdminRolePermissions: claves "module.action" del rol "Admin" actual (Fase 6 §7) — para que
	// un humano confirme que siguen siendo las esperadas ANTES de asignárselas a nadie.
	AdminRolePermissions []string

	// Blocked == true si la migración real NO debe ejecutarse todavía (ver Fase 6 §6: sin ningún
	// superadmin operativo). El propio dry-run SIEMPRE completa y reporta — es responsabilidad del
	// caller de la futura migración real negarse a proceder si Blocked es true.
	Blocked     bool
	BlockReason string
}

// DryRunUserRoleMigration genera el plan de migración. Nunca modifica ninguna fila.
func DryRunUserRoleMigration(db *gorm.DB) (*MigrationDryRunReport, error) {
	report := &MigrationDryRunReport{RoleIDsByName: map[string]uint{}}

	if err := resolveSystemRoles(db, report); err != nil {
		return nil, err
	}
	if err := inventorySuperadmins(db, report); err != nil {
		return nil, err
	}
	if report.Superadmins.Active == 0 {
		report.Blocked = true
		report.BlockReason = "no existe ningún superadmin operativo (Role=superadmin, Active=true, no eliminado) — la migración real NO debe ejecutarse"
	}

	if adminRoleID, ok := report.RoleIDsByName["Admin"]; ok {
		roleSvc := NewSARoleService(db)
		keys, err := roleSvc.GetRolePermissionKeys(adminRoleID)
		if err != nil {
			return nil, err
		}
		report.AdminRolePermissions = keys
	}

	var users []database.SuperAdminUser
	if err := db.Unscoped().Order("id ASC").Find(&users).Error; err != nil {
		return nil, err
	}

	emailCounts := make(map[string]int, len(users))
	for _, u := range users {
		emailCounts[u.Email]++
	}

	existingRoleIDs, err := existingSARoleIDs(db, users)
	if err != nil {
		return nil, err
	}

	for _, u := range users {
		report.Rows = append(report.Rows, buildMigrationPlanRow(u, report, emailCounts, existingRoleIDs))
	}

	return report, nil
}

// existingSARoleIDs resuelve, de una sola consulta, cuáles de los RoleID actualmente referenciados
// por SuperAdminUser existen de verdad en sa_roles — necesario para distinguir "apunta a un rol
// personalizado válido" de "apunta a un rol inexistente" (huérfano), sin asumir que el único
// universo posible de roles son los tres de sistema.
func existingSARoleIDs(db *gorm.DB, users []database.SuperAdminUser) (map[uint]bool, error) {
	referenced := make(map[uint]bool)
	for _, u := range users {
		if u.RoleID != nil {
			referenced[*u.RoleID] = true
		}
	}
	if len(referenced) == 0 {
		return map[uint]bool{}, nil
	}
	ids := make([]uint, 0, len(referenced))
	for id := range referenced {
		ids = append(ids, id)
	}
	var roles []database.SARole
	if err := db.Where("id IN ?", ids).Find(&roles).Error; err != nil {
		return nil, err
	}
	exists := make(map[uint]bool, len(roles))
	for _, r := range roles {
		exists[r.ID] = true
	}
	return exists, nil
}

func resolveSystemRoles(db *gorm.DB, report *MigrationDryRunReport) error {
	for _, name := range systemRoleNames {
		var roles []database.SARole
		if err := db.Where("name = ?", name).Find(&roles).Error; err != nil {
			return err
		}
		switch len(roles) {
		case 0:
			report.MissingSystemRoles = append(report.MissingSystemRoles, name)
		case 1:
			report.RoleIDsByName[name] = roles[0].ID
		default:
			report.DuplicateSystemRoles = append(report.DuplicateSystemRoles, name)
		}
	}
	return nil
}

func inventorySuperadmins(db *gorm.DB, report *MigrationDryRunReport) error {
	var active, inactive, softDeleted, total int64

	// Active/Inactive: solo filas NO eliminadas (el scope de soft-delete de GORM excluye
	// deleted_at automáticamente en cualquier consulta sin Unscoped()).
	if err := db.Model(&database.SuperAdminUser{}).
		Where("role = ? AND active = ?", "superadmin", true).Count(&active).Error; err != nil {
		return err
	}
	if err := db.Model(&database.SuperAdminUser{}).
		Where("role = ? AND active = ?", "superadmin", false).Count(&inactive).Error; err != nil {
		return err
	}
	// SoftDeleted: solo las filas eliminadas.
	if err := db.Unscoped().Model(&database.SuperAdminUser{}).
		Where("role = ? AND deleted_at IS NOT NULL", "superadmin").Count(&softDeleted).Error; err != nil {
		return err
	}
	// Total: todas, eliminadas o no.
	if err := db.Unscoped().Model(&database.SuperAdminUser{}).
		Where("role = ?", "superadmin").Count(&total).Error; err != nil {
		return err
	}

	report.Superadmins = SuperadminInventory{
		Total:       int(total),
		Active:      int(active),
		Inactive:    int(inactive),
		SoftDeleted: int(softDeleted),
	}
	return nil
}

func buildMigrationPlanRow(u database.SuperAdminUser, report *MigrationDryRunReport, emailCounts map[string]int, existingRoleIDs map[uint]bool) MigrationPlanRow {
	row := MigrationPlanRow{
		UserID:        u.ID,
		Email:         u.Email,
		Role:          u.Role,
		CurrentRoleID: u.RoleID,
		Active:        u.Active,
		Deleted:       u.DeletedAt.Valid,
	}

	if emailCounts[u.Email] > 1 {
		row.Anomalies = append(row.Anomalies, "email duplicado entre varios usuarios")
	}

	if row.Deleted {
		row.Anomalies = append(row.Anomalies, "usuario eliminado (soft-delete): se omite, sin propuesta")
		row.Reason = "usuario eliminado — no se propone ningún cambio"
		return row
	}

	if !u.Active {
		// No es una anomalía bloqueante (§10.6: "reportar estado pero no cambiarlo") — se sigue
		// evaluando la propuesta normal más abajo, solo se deja constancia del estado.
		row.Anomalies = append(row.Anomalies, "usuario inactivo (Active=false) — se reporta el estado, no se cambia")
	}

	if u.RoleID != nil {
		row.Anomalies = append(row.Anomalies, "ya tiene RoleID asignado — conflicto, no se propone cambio")
		if !existingRoleIDs[*u.RoleID] {
			row.Anomalies = append(row.Anomalies, "el RoleID actual apunta a un rol inexistente")
		}
		row.Reason = "conflicto: RoleID ya asignado, requiere revisión manual"
		return row
	}

	switch u.Role {
	case "admin":
		roleID, ok := report.RoleIDsByName["Admin"]
		if !ok {
			row.Anomalies = append(row.Anomalies, "no se puede proponer RoleID: el rol \"Admin\" no se resolvió de forma unívoca")
			row.Reason = "bloqueado: falta o hay duplicados del rol de sistema Admin"
			return row
		}
		row.ProposedRoleID = &roleID
		row.ProposedRoleName = "Admin"
		row.Reason = "Role=admin → se propone el rol granular \"Admin\""
	case "superadmin":
		row.ProposedRoleID = nil
		row.ProposedRoleName = ""
		row.Reason = "Role=superadmin: bypass total, no requiere rol granular (RoleID permanece NULL)"
	default:
		row.Anomalies = append(row.Anomalies, "Role desconocido o vacío (ni admin ni superadmin)")
		row.Reason = "bloqueado: Role no reconocido, requiere revisión manual"
	}

	return row
}
