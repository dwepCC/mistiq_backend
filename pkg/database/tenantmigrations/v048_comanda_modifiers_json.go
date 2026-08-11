package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V048ComandaModifiersJSON agrega modifiers_json a tenant_comandas (variantes y extras).
type V048ComandaModifiersJSON struct{}

func (V048ComandaModifiersJSON) Version() int { return 48 }
func (V048ComandaModifiersJSON) Name() string { return "comanda_modifiers_json" }

func (V048ComandaModifiersJSON) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_comandas") {
		return nil
	}
	if db.Migrator().HasColumn("tenant_comandas", "modifiers_json") {
		return nil
	}
	if err := db.Exec(`ALTER TABLE tenant_comandas ADD COLUMN modifiers_json TEXT NULL`).Error; err != nil {
		return fmt.Errorf("tenant_comandas.modifiers_json: %w", err)
	}
	return nil
}
