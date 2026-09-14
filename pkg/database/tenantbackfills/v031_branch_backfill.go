package tenantbackfills

import (
	"fmt"

	"gorm.io/gorm"
)

// V031BranchBackfill datos iniciales multi-sucursal (run-once).
type V031BranchBackfill struct{}

func (V031BranchBackfill) Version() int { return 31 }
func (V031BranchBackfill) Name() string { return "multi_branch_backfill" }

// Repeatable: ver tenantbackfills.Repeatable. Igual que V036, depende de schema (columna
// tenant_users.home_branch_id) desplegado de forma incremental/asíncrona por el cron de
// migración — una corrida puede fallar con "esquema no listo" mientras ese schema aún se
// despliega para ese tenant, y sin esto quedaría trabada para siempre por el candado run-once.
// Todos sus UPDATE llevan guarda propia (IS NULL OR = 0 / rol específico), así que repetirla no
// duplica ni corrompe nada ya resuelto.
func (V031BranchBackfill) Repeatable() bool { return true }

func (V031BranchBackfill) Run(db *gorm.DB) error {
	if !hasColumn(db, "tenant_users", "home_branch_id") {
		return fmt.Errorf("backfill: esquema multi-sucursal no listo")
	}

	mainID, err := resolveMainBranchID(db)
	if err != nil {
		return err
	}

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
	if err := db.Exec(`
		UPDATE tenant_users u
		INNER JOIN tenant_roles r ON r.id = u.role_id
		SET u.can_switch_branch = 1
		WHERE r.name = 'Administrador'
	`).Error; err != nil {
		return fmt.Errorf("backfill can_switch_branch: %w", err)
	}

	if hasTable(db, "tenant_restaurant_floors") && hasColumn(db, "tenant_restaurant_floors", "branch_id") {
		if err := db.Exec(
			`UPDATE tenant_restaurant_floors SET branch_id = ? WHERE branch_id IS NULL OR branch_id = 0`,
			mainID,
		).Error; err != nil {
			return fmt.Errorf("backfill floors: %w", err)
		}
	}
	if hasTable(db, "tenant_restaurant_tables") && hasColumn(db, "tenant_restaurant_tables", "branch_id") {
		if err := db.Exec(
			`UPDATE tenant_restaurant_tables SET branch_id = ? WHERE branch_id IS NULL OR branch_id = 0`,
			mainID,
		).Error; err != nil {
			return fmt.Errorf("backfill tables: %w", err)
		}
	}
	return nil
}

type v031Branch struct {
	ID     uint `gorm:"primaryKey"`
	IsMain bool `gorm:"column:is_main"`
	Active bool `gorm:"column:active"`
}

func (v031Branch) TableName() string { return "tenant_branches" }

func resolveMainBranchID(db *gorm.DB) (uint, error) {
	var mainID uint = 1
	var main v031Branch
	if err := db.Where("is_main = ? AND active = ?", true, true).First(&main).Error; err == nil {
		return main.ID, nil
	}
	if err := db.Where("active = ?", true).Order("id ASC").First(&main).Error; err == nil {
		return main.ID, nil
	}
	return mainID, nil
}

func hasTable(db *gorm.DB, table string) bool {
	return db.Migrator().HasTable(table)
}

func hasColumn(db *gorm.DB, table, column string) bool {
	return db.Migrator().HasColumn(table, column)
}
