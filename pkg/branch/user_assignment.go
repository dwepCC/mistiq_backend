package branch

import (
	"errors"
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// UserBranchesReady indica si existe la tabla de asignaciones.
func UserBranchesReady(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	return database.CachedSchemaProbe(db, "tenant_user_branches", func() bool {
		return db.Migrator().HasTable(&database.TenantUserBranch{})
	})
}

// GetUserAssignedBranchIDs sucursales asignadas al usuario (activas).
func GetUserAssignedBranchIDs(db *gorm.DB, userID uint) ([]uint, error) {
	if userID == 0 || !UserBranchesReady(db) {
		return nil, nil
	}
	var ids []uint
	err := db.Model(&database.TenantUserBranch{}).
		Select("tenant_user_branches.branch_id").
		Joins("JOIN tenant_branches ON tenant_branches.id = tenant_user_branches.branch_id").
		Where("tenant_user_branches.user_id = ? AND tenant_branches.active = ? AND tenant_branches.deleted_at IS NULL", userID, true).
		Order("tenant_branches.is_main DESC, tenant_branches.name ASC").
		Pluck("tenant_user_branches.branch_id", &ids).Error
	return ids, err
}

// UsersOnlyInBranch nombres de los usuarios que se quedarían sin ninguna sucursal si se elimina branchID:
// los que solo la tienen asignada (o, sin asignaciones N:N, la tienen como sucursal de origen).
func UsersOnlyInBranch(db *gorm.DB, branchID uint) ([]string, error) {
	var users []database.TenantUser
	if err := db.Find(&users).Error; err != nil {
		return nil, err
	}
	var names []string
	for _, u := range users {
		ids, err := GetUserAssignedBranchIDs(db, u.ID)
		if err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			others := 0
			for _, id := range ids {
				if id != branchID {
					others++
				}
			}
			if others == 0 {
				names = append(names, u.Name)
			}
			continue
		}
		legacy := uint(0)
		if u.HomeBranchID != nil && *u.HomeBranchID > 0 {
			legacy = *u.HomeBranchID
		} else if u.BranchID != nil && *u.BranchID > 0 {
			legacy = *u.BranchID
		}
		if legacy == branchID {
			names = append(names, u.Name)
		}
	}
	return names, nil
}

// DetachBranchFromUsers quita branchID de las asignaciones de todos los usuarios y reubica la sucursal de
// origen de quienes la tenían como tal en la primera que les queda. Llamar dentro de la transacción que
// elimina la sucursal, y solo después de comprobar con UsersOnlyInBranch que nadie se queda sin ninguna.
func DetachBranchFromUsers(tx *gorm.DB, branchID uint) error {
	if UserBranchesReady(tx) {
		if err := tx.Where("branch_id = ?", branchID).Delete(&database.TenantUserBranch{}).Error; err != nil {
			return err
		}
	}
	var users []database.TenantUser
	if err := tx.Where("home_branch_id = ? OR branch_id = ?", branchID, branchID).Find(&users).Error; err != nil {
		return err
	}
	for _, u := range users {
		ids, err := GetUserAssignedBranchIDs(tx, u.ID)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			continue
		}
		if err := tx.Model(&database.TenantUser{}).Where("id = ?", u.ID).Updates(map[string]interface{}{
			"home_branch_id":    ids[0],
			"branch_id":         ids[0],
			"can_switch_branch": len(ids) > 1,
		}).Error; err != nil {
			return err
		}
		if err := BumpSessionVersion(tx, u.ID); err != nil {
			return err
		}
	}
	return nil
}

// ListUserBranchBriefs sucursales asignadas (vacío si no hay tabla o filas).
func ListUserBranchBriefs(db *gorm.DB, userID uint) ([]BranchBrief, error) {
	ids, err := GetUserAssignedBranchIDs(db, userID)
	if err != nil {
		return nil, err
	}
	out := make([]BranchBrief, 0, len(ids))
	for _, id := range ids {
		b, err := GetBranchBrief(db, id)
		if err != nil {
			continue
		}
		out = append(out, *b)
	}
	return out, nil
}

// UserHasBranchAccess true si el usuario puede operar en la sucursal.
func UserHasBranchAccess(db *gorm.DB, userID, branchID uint) (bool, error) {
	if branchID == 0 {
		return false, nil
	}
	if !UserBranchesReady(db) {
		return true, nil
	}
	ids, err := GetUserAssignedBranchIDs(db, userID)
	if err != nil {
		return false, err
	}
	if len(ids) == 0 {
		return true, nil
	}
	for _, id := range ids {
		if id == branchID {
			return true, nil
		}
	}
	return false, nil
}

// ValidateBranchIDsExist comprueba que todas las sucursales existan y estén activas.
func ValidateBranchIDsExist(db *gorm.DB, branchIDs []uint) error {
	if len(branchIDs) == 0 {
		return errors.New("seleccione al menos una sucursal")
	}
	seen := make(map[uint]struct{}, len(branchIDs))
	unique := make([]uint, 0, len(branchIDs))
	for _, id := range branchIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return errors.New("seleccione al menos una sucursal")
	}
	var count int64
	if err := db.Model(&database.TenantBranch{}).Where("id IN ? AND active = ?", unique, true).Count(&count).Error; err != nil {
		return err
	}
	if int(count) != len(unique) {
		return errors.New("una o más sucursales no son válidas")
	}
	return nil
}

// ResolveDisplayBranchIDs sucursales para UI: asignaciones N:N o home_branch_id legacy.
func ResolveDisplayBranchIDs(db *gorm.DB, userID uint, homeBranchID, legacyBranchID *uint) ([]uint, error) {
	ids, err := GetUserAssignedBranchIDs(db, userID)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		return ids, nil
	}
	if homeBranchID != nil && *homeBranchID > 0 {
		return []uint{*homeBranchID}, nil
	}
	if legacyBranchID != nil && *legacyBranchID > 0 {
		return []uint{*legacyBranchID}, nil
	}
	return nil, nil
}

// SetUserAssignedBranches reemplaza asignaciones y actualiza home / can_switch.
func SetUserAssignedBranches(db *gorm.DB, userID uint, branchIDs []uint, bumpSession bool) error {
	if userID == 0 {
		return errors.New("usuario inválido")
	}
	if !UserBranchesReady(db) {
		return errors.New("esquema de sucursales por usuario no disponible; ejecute migrate-fleet")
	}
	if err := ValidateBranchIDsExist(db, branchIDs); err != nil {
		return err
	}
	seen := make(map[uint]struct{})
	unique := make([]uint, 0, len(branchIDs))
	for _, id := range branchIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&database.TenantUserBranch{}).Error; err != nil {
			return err
		}
		for _, bid := range unique {
			if err := tx.Create(&database.TenantUserBranch{UserID: userID, BranchID: bid}).Error; err != nil {
				return err
			}
		}
		home := unique[0]
		canSwitch := len(unique) > 1
		updates := map[string]interface{}{
			"home_branch_id":    home,
			"branch_id":         home,
			"can_switch_branch": canSwitch,
		}
		if err := tx.Model(&database.TenantUser{}).Where("id = ?", userID).Updates(updates).Error; err != nil {
			return fmt.Errorf("actualizando usuario: %w", err)
		}
		if bumpSession {
			return BumpSessionVersion(tx, userID)
		}
		return nil
	})
}

// ResolveUserSessionBranchID sucursal activa al iniciar sesión (respeta asignaciones).
func ResolveUserSessionBranchID(db *gorm.DB, u *database.TenantUser) (uint, error) {
	if u == nil {
		return 0, errors.New("usuario inválido")
	}
	if UserBranchesReady(db) {
		ids, err := GetUserAssignedBranchIDs(db, u.ID)
		if err != nil {
			return 0, err
		}
		if len(ids) > 0 {
			home, err := ResolveHomeBranchID(db, u)
			if err != nil {
				return ids[0], nil
			}
			for _, id := range ids {
				if id == home {
					return home, nil
				}
			}
			return ids[0], nil
		}
	}
	return ResolveHomeBranchID(db, u)
}

// UserCanSwitchBranch indica si el usuario puede cambiar de sucursal en sesión (ver UserCanSwitchBranchAuth).
func UserCanSwitchBranch(db *gorm.DB, roleName string, u *database.TenantUser) bool {
	return UserCanSwitchBranchAuth(db, roleName, u, "")
}

// UserCanSwitchBranchAuth: puede cambiar de sucursal quien ve todas (branches.view_all), quien tiene el permiso
// explícito branches.switch, o quien tiene 2+ sucursales asignadas (solo entre ellas). El POS por PIN queda fijo en su
// sucursal salvo permiso explícito branches.switch. Tenants sin V176 conservan la regla anterior (flag / rol).
func UserCanSwitchBranchAuth(db *gorm.DB, roleName string, u *database.TenantUser, authMethod string) bool {
	if u == nil {
		return false
	}
	if !PermissionCatalogReady(db) {
		if IsTenantAdmin(roleName) || u.CanSwitchBranch {
			return true
		}
	} else {
		if RoleViewsAll(db, u.RoleID, roleName, authMethod) || RoleHasBranchPermission(db, u.RoleID, "switch") {
			return true
		}
		if authMethod == authMethodPIN {
			return false
		}
	}
	if UserBranchesReady(db) {
		ids, err := GetUserAssignedBranchIDs(db, u.ID)
		if err == nil && len(ids) > 1 {
			return true
		}
	}
	return false
}

// UserHasAnyBranch indica si el usuario tiene alguna sucursal configurada (asignación N:N, home o branch_id).
func UserHasAnyBranch(db *gorm.DB, u *database.TenantUser) bool {
	if u == nil {
		return false
	}
	if u.HomeBranchID != nil && *u.HomeBranchID > 0 {
		return true
	}
	if u.BranchID != nil && *u.BranchID > 0 {
		return true
	}
	ids, err := GetUserAssignedBranchIDs(db, u.ID)
	return err == nil && len(ids) > 0
}
