package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V041AutomaticSend añade automatic_send en tenant_company_configs (default true).
type V041AutomaticSend struct{}

func (V041AutomaticSend) Version() int { return 41 }
func (V041AutomaticSend) Name() string { return "automatic_send_column" }

type v041CompanyConfigAutoSend struct {
	AutomaticSend bool `gorm:"column:automatic_send;default:true"`
}

func (v041CompanyConfigAutoSend) TableName() string { return "tenant_company_configs" }

func (V041AutomaticSend) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_company_configs") {
		return nil
	}
	st := &v041CompanyConfigAutoSend{}
	if !db.Migrator().HasColumn(st, "AutomaticSend") {
		if err := db.Migrator().AddColumn(st, "AutomaticSend"); err != nil {
			return fmt.Errorf("tenant_company_configs.automatic_send: %w", err)
		}
	}
	// Rollout: envío automático activo por defecto en tenants existentes.
	return db.Exec("UPDATE tenant_company_configs SET automatic_send = TRUE").Error
}
