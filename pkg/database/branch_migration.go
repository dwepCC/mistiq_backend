package database

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

const (
	branchMultiBackfillPatchKey = "branch_multi_backfill_v1"
)

// TenantSchemaPatch registra backfills / parches de datos ejecutados una sola vez por tenant.
type TenantSchemaPatch struct {
	ID        uint      `gorm:"primaryKey"`
	PatchKey  string    `gorm:"size:64;uniqueIndex;not null"`
	AppliedAt time.Time `gorm:"autoCreateTime"`
}

func (TenantSchemaPatch) TableName() string { return "tenant_schema_patches" }

// TenantBranchMultiSchemaReady indica si el tenant ya tiene columnas multi-sucursal en tenant_users.
func TenantBranchMultiSchemaReady(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	return CachedSchemaProbe(db, "tenant_users.home_branch_id", func() bool {
		return db.Migrator().HasColumn(&TenantUser{}, "HomeBranchID")
	})
}

// TenantBranchSessionVersionReady indica si existe branch_session_version.
func TenantBranchSessionVersionReady(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	return CachedSchemaProbe(db, "tenant_users.branch_session_version", func() bool {
		return db.Migrator().HasColumn(&TenantUser{}, "BranchSessionVersion")
	})
}

// TenantRestaurantBranchColumnReady indica si floors/tables tienen branch_id.
func TenantRestaurantBranchColumnReady(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	m := db.Migrator()
	return m.HasTable(&TenantRestaurantFloor{}) && m.HasColumn(&TenantRestaurantFloor{}, "BranchID")
}

// IsBranchMultiBackfillApplied true si el backfill de datos multi-sucursal ya se aplicó (run-once).
func IsBranchMultiBackfillApplied(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	m := db.Migrator()
	if !m.HasTable(&TenantSchemaPatch{}) {
		return false
	}
	var n int64
	db.Model(&TenantSchemaPatch{}).Where("patch_key = ?", branchMultiBackfillPatchKey).Count(&n)
	return n > 0
}

func markBranchMultiBackfillApplied(db *gorm.DB) error {
	if IsBranchMultiBackfillApplied(db) {
		return nil
	}
	return db.Create(&TenantSchemaPatch{PatchKey: branchMultiBackfillPatchKey}).Error
}

// ensureBranchMultiColumnsSchema solo DDL (ADD COLUMN si falta). Sin UPDATE masivos.
func ensureBranchMultiColumnsSchema(db *gorm.DB) error {
	mig := db.Migrator()

	u := &TenantUser{}
	if mig.HasTable(u) {
		if !mig.HasColumn(u, "HomeBranchID") {
			if err := mig.AddColumn(u, "HomeBranchID"); err != nil {
				return fmt.Errorf("add tenant_users.home_branch_id: %w", err)
			}
		}
		if !mig.HasColumn(u, "CanSwitchBranch") {
			if err := mig.AddColumn(u, "CanSwitchBranch"); err != nil {
				return fmt.Errorf("add tenant_users.can_switch_branch: %w", err)
			}
		}
		if !mig.HasColumn(u, "BranchSessionVersion") {
			if err := mig.AddColumn(u, "BranchSessionVersion"); err != nil {
				return fmt.Errorf("add tenant_users.branch_session_version: %w", err)
			}
		}
	}

	floor := &TenantRestaurantFloor{}
	if mig.HasTable(floor) && !mig.HasColumn(floor, "BranchID") {
		if err := mig.AddColumn(floor, "BranchID"); err != nil {
			return fmt.Errorf("add tenant_restaurant_floors.branch_id: %w", err)
		}
	}

	tbl := &TenantRestaurantTable{}
	if mig.HasTable(tbl) && !mig.HasColumn(tbl, "BranchID") {
		if err := mig.AddColumn(tbl, "BranchID"); err != nil {
			return fmt.Errorf("add tenant_restaurant_tables.branch_id: %w", err)
		}
	}

	return nil
}

// BackfillBranchMultiData aplica UPDATEs de datos multi-sucursal (ejecutar una sola vez por tenant).
func BackfillBranchMultiData(db *gorm.DB) error {
	if !TenantBranchMultiSchemaReady(db) {
		return fmt.Errorf("backfill branch multi: esquema no listo (falta home_branch_id)")
	}

	mainID, err := resolveMainBranchIDForBackfill(db)
	if err != nil {
		return err
	}

	// home_branch_id desde branch_id legacy
	if err := db.Exec(`
		UPDATE tenant_users SET home_branch_id = branch_id
		WHERE (home_branch_id IS NULL OR home_branch_id = 0) AND branch_id IS NOT NULL AND branch_id > 0
	`).Error; err != nil {
		return fmt.Errorf("backfill home_branch from branch_id: %w", err)
	}
	if err := db.Exec(
		`UPDATE tenant_users SET home_branch_id = ? WHERE home_branch_id IS NULL OR home_branch_id = 0`,
		mainID,
	).Error; err != nil {
		return fmt.Errorf("backfill home_branch main: %w", err)
	}

	// Administradores pueden cambiar sucursal (solo en backfill run-once)
	if err := db.Exec(`
		UPDATE tenant_users u
		INNER JOIN tenant_roles r ON r.id = u.role_id
		SET u.can_switch_branch = 1
		WHERE r.name = 'Administrador'
	`).Error; err != nil {
		return fmt.Errorf("backfill can_switch_branch admins: %w", err)
	}

	mig := db.Migrator()
	floor := &TenantRestaurantFloor{}
	if mig.HasTable(floor) && mig.HasColumn(floor, "BranchID") {
		if err := db.Exec(
			`UPDATE tenant_restaurant_floors SET branch_id = ? WHERE branch_id IS NULL OR branch_id = 0`,
			mainID,
		).Error; err != nil {
			return fmt.Errorf("backfill floors branch_id: %w", err)
		}
	}
	tbl := &TenantRestaurantTable{}
	if mig.HasTable(tbl) && mig.HasColumn(tbl, "BranchID") {
		if err := db.Exec(
			`UPDATE tenant_restaurant_tables SET branch_id = ? WHERE branch_id IS NULL OR branch_id = 0`,
			mainID,
		).Error; err != nil {
			return fmt.Errorf("backfill tables branch_id: %w", err)
		}
	}

	return nil
}

func resolveMainBranchIDForBackfill(db *gorm.DB) (uint, error) {
	var mainID uint = 1
	var main TenantBranch
	if err := db.Where("is_main = ? AND active = ?", true, true).First(&main).Error; err == nil {
		return main.ID, nil
	}
	if err := db.Where("active = ?", true).Order("id ASC").First(&main).Error; err == nil {
		return main.ID, nil
	}
	return mainID, nil
}

// RunBranchMultiBackfillOnce ejecuta backfill V031 run-once (history + patch legacy).
func RunBranchMultiBackfillOnce(db *gorm.DB) error {
	if applied, err := isBackfillV31Applied(db); err != nil {
		return err
	} else if applied {
		return nil
	}
	if IsBranchMultiBackfillApplied(db) {
		return nil
	}
	if !TenantBranchMultiSchemaReady(db) {
		return nil
	}
	if err := BackfillBranchMultiData(db); err != nil {
		return err
	}
	return markBranchMultiBackfillApplied(db)
}

func isBackfillV31Applied(db *gorm.DB) (bool, error) {
	if !db.Migrator().HasTable(&TenantMigrationHistory{}) {
		return false, nil
	}
	var n int64
	err := db.Model(&TenantMigrationHistory{}).
		Where("version = ? AND type = ? AND success = ?", 31, MigrationHistoryTypeBackfill, true).
		Count(&n).Error
	return n > 0, err
}

// tenantUserLegacySelect columnas seguras cuando el tenant aún no tiene migración multi-sucursal.
const tenantUserLegacySelect = "id, role_id, branch_id, name, email, active, password, phone"

// LoadTenantUserForBranch carga usuario para contexto de sucursal sin fallar si faltan columnas nuevas.
// legacy=true: solo branch_id; sin home_branch_id ni branch_session_version en BD.
func LoadTenantUserForBranch(db *gorm.DB, userID uint) (*TenantUser, bool, error) {
	if userID == 0 {
		return nil, false, fmt.Errorf("user id requerido")
	}
	if TenantBranchMultiSchemaReady(db) {
		var u TenantUser
		if err := db.First(&u, userID).Error; err != nil {
			return nil, false, err
		}
		return &u, false, nil
	}
	var u TenantUser
	if err := db.Model(&TenantUser{}).Select(tenantUserLegacySelect).Where("id = ?", userID).First(&u).Error; err != nil {
		return nil, true, err
	}
	return &u, true, nil
}

// LoadTenantUserForBranchByEmail igual que LoadTenantUserForBranch pero por email (login).
func LoadTenantUserForBranchByEmail(db *gorm.DB, email string) (*TenantUser, bool, error) {
	if TenantBranchMultiSchemaReady(db) {
		var u TenantUser
		if err := db.Where("email = ? AND active = ?", email, true).First(&u).Error; err != nil {
			return nil, false, err
		}
		return &u, false, nil
	}
	var u TenantUser
	if err := db.Model(&TenantUser{}).Select(tenantUserLegacySelect).
		Where("email = ? AND active = ?", email, true).First(&u).Error; err != nil {
		return nil, true, err
	}
	return &u, true, nil
}

// PersistUserBranchFieldsOnLogin actualiza home_branch_id solo si el esquema ya lo soporta.
func PersistUserBranchFieldsOnLogin(db *gorm.DB, userID uint, homeBranchID uint, canSwitch bool) error {
	if !TenantBranchMultiSchemaReady(db) {
		return nil
	}
	updates := map[string]interface{}{
		"home_branch_id":    homeBranchID,
		"branch_id":         homeBranchID,
		"can_switch_branch": canSwitch,
	}
	return db.Model(&TenantUser{}).Where("id = ?", userID).Updates(updates).Error
}
