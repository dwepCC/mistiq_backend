package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v054ModifierGroup struct {
	Kind string `gorm:"size:20;default:extra"`
}

func (v054ModifierGroup) TableName() string { return "tenant_modifier_groups" }

// V054ModifierGroupKind distingue presentaciones (reemplazan precio) de extras (suman al precio).
type V054ModifierGroupKind struct{}

func (V054ModifierGroupKind) Version() int { return 54 }
func (V054ModifierGroupKind) Name() string { return "modifier_group_kind" }

func (V054ModifierGroupKind) Up(db *gorm.DB) error {
	mig := db.Migrator()
	row := &v054ModifierGroup{}
	if !mig.HasTable(row) {
		return nil
	}
	if !mig.HasColumn(row, "Kind") {
		if err := mig.AddColumn(row, "Kind"); err != nil {
			return fmt.Errorf("tenant_modifier_groups.kind: %w", err)
		}
	}
	if err := db.Exec(`UPDATE tenant_modifier_groups SET kind = 'presentation' WHERE required = 1 AND multi_select = 0`).Error; err != nil {
		return err
	}
	return db.Exec(`UPDATE tenant_modifier_groups SET kind = 'extra' WHERE kind IS NULL OR kind = ''`).Error
}
