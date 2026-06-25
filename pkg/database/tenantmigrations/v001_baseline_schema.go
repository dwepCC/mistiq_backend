package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V001BaselineSchema esquema inicial completo del tenant (antes AutoMigrate en provision).
type V001BaselineSchema struct{}

func (V001BaselineSchema) Version() int  { return 1 }
func (V001BaselineSchema) Name() string { return "baseline_schema" }

func (V001BaselineSchema) Up(db *gorm.DB) error {
	return database.ApplyBaselineSchema(db)
}
