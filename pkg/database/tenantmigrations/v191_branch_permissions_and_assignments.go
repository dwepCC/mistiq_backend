package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V191BranchPermissionsAndAssignments prepara el aislamiento por sucursal basado en permisos:
//
//  1. Siembra branches.view_all / branches.switch / branches.manage. A partir de aquí "ver todas las sucursales" es el
//     permiso branches.view_all y no el nombre del rol "Administrador".
//  2. Backfill de permisos sin cambiar el acceso de nadie el día del deploy: el rol Administrador recibe los tres, y
//     cualquier otro rol con usuarios marcados can_switch_branch recibe branches.switch.
//  3. Backfill de sucursal: todo usuario sin ninguna asignación (tenant_user_branches) recibe su sucursal de origen
//     (home_branch_id, luego branch_id) o, en su defecto, la sucursal principal. Solo después de esto el servidor bloquea
//     a quien no tenga sucursal.
//
// No elimina ni modifica filas existentes: solo agrega. Idempotente. Catálogo espejo: role_service.go (SeedPermissions).
type V191BranchPermissionsAndAssignments struct{}

func (V191BranchPermissionsAndAssignments) Version() int { return 191 }
func (V191BranchPermissionsAndAssignments) Name() string { return "branch_permissions_and_assignments" }

func (V191BranchPermissionsAndAssignments) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) ||
		!db.Migrator().HasTable(&database.TenantRolePermission{}) {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		labels := map[string]string{
			"view_all": "Sucursales: ver todas las sucursales (consulta consolidada, solo lectura)",
			"switch":   "Sucursales: cambiar entre las sucursales asignadas",
			"manage":   "Sucursales: crear/editar sucursales y asignar acceso total",
		}
		perms := map[string]database.TenantPermission{}
		for _, action := range []string{"view_all", "switch", "manage"} {
			var p database.TenantPermission
			if err := tx.Where(database.TenantPermission{Module: "branches", Action: action}).
				Attrs(database.TenantPermission{Label: labels[action]}).
				FirstOrCreate(&p).Error; err != nil {
				return err
			}
			perms[action] = p
		}

		// Administrador: los tres permisos.
		var admins []database.TenantRole
		if err := tx.Where("name = ?", "Administrador").Find(&admins).Error; err != nil {
			return err
		}
		for _, r := range admins {
			for _, p := range perms {
				if err := grantIfMissing(tx, r.ID, p.ID); err != nil {
					return err
				}
			}
		}

		if !tx.Migrator().HasTable(&database.TenantUser{}) || !tx.Migrator().HasColumn(&database.TenantUser{}, "can_switch_branch") {
			return nil
		}
		// Otros roles con usuarios que hoy cambian de sucursal: conservan esa capacidad como permiso explícito.
		var roleIDs []uint
		if err := tx.Model(&database.TenantUser{}).Where("can_switch_branch = ?", true).
			Distinct().Pluck("role_id", &roleIDs).Error; err != nil {
			return err
		}
		for _, rid := range roleIDs {
			if rid == 0 {
				continue
			}
			if err := grantIfMissing(tx, rid, perms["switch"].ID); err != nil {
				return err
			}
		}

		// Backfill de sucursal para usuarios sin asignaciones.
		if !tx.Migrator().HasTable(&database.TenantUserBranch{}) || !tx.Migrator().HasTable(&database.TenantBranch{}) ||
			!tx.Migrator().HasColumn(&database.TenantUser{}, "home_branch_id") {
			return nil
		}
		var main database.TenantBranch
		if err := tx.Where("is_main = ? AND active = ?", true, true).First(&main).Error; err != nil {
			if err2 := tx.Where("active = ?", true).Order("id ASC").First(&main).Error; err2 != nil {
				return nil // tenant sin sucursales activas: nada que asignar
			}
		}
		var users []database.TenantUser
		if err := tx.Where("id NOT IN (?)", tx.Model(&database.TenantUserBranch{}).Select("user_id")).Find(&users).Error; err != nil {
			return err
		}
		for _, u := range users {
			target := main.ID
			if u.HomeBranchID != nil && *u.HomeBranchID > 0 {
				target = *u.HomeBranchID
			} else if u.BranchID != nil && *u.BranchID > 0 {
				target = *u.BranchID
			}
			var exists int64
			tx.Model(&database.TenantBranch{}).Where("id = ? AND active = ?", target, true).Count(&exists)
			if exists == 0 {
				target = main.ID
			}
			if err := tx.Create(&database.TenantUserBranch{UserID: u.ID, BranchID: target}).Error; err != nil {
				return err
			}
			if err := tx.Model(&database.TenantUser{}).Where("id = ? AND (home_branch_id IS NULL OR home_branch_id = 0)", u.ID).
				Update("home_branch_id", target).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
