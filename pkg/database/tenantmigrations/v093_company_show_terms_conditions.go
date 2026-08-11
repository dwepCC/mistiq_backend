package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v093CompanyConfig struct {
	ShowTermsConditions bool `gorm:"column:show_terms_conditions;default:false"`
}

func (v093CompanyConfig) TableName() string { return "tenant_company_configs" }

// V093CompanyShowTermsConditions preferencia global: mostrar términos en ventas futuras.
type V093CompanyShowTermsConditions struct{}

func (V093CompanyShowTermsConditions) Version() int { return 93 }
func (V093CompanyShowTermsConditions) Name() string { return "company_show_terms_conditions" }

func (V093CompanyShowTermsConditions) Up(db *gorm.DB) error {
	mig := db.Migrator()
	st := &v093CompanyConfig{}
	if !mig.HasColumn(st, "ShowTermsConditions") {
		if err := mig.AddColumn(st, "ShowTermsConditions"); err != nil {
			return fmt.Errorf("add tenant_company_configs.show_terms_conditions: %w", err)
		}
	}
	return nil
}
