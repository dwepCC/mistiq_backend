package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v091CompanyConfig struct {
	TermsAndConditions string `gorm:"column:terms_and_conditions;type:text"`
}

func (v091CompanyConfig) TableName() string { return "tenant_company_configs" }

type v091Quotation struct {
	ShowTermsConditions bool `gorm:"column:show_terms_conditions;default:false"`
}

func (v091Quotation) TableName() string { return "tenant_quotations" }

// V091CompanyTermsConditions términos globales de empresa y flag por cotización.
type V091CompanyTermsConditions struct{}

func (V091CompanyTermsConditions) Version() int { return 91 }
func (V091CompanyTermsConditions) Name() string { return "company_terms_conditions" }

func (V091CompanyTermsConditions) Up(db *gorm.DB) error {
	mig := db.Migrator()
	stCompany := &v091CompanyConfig{}
	if !mig.HasColumn(stCompany, "TermsAndConditions") {
		if err := mig.AddColumn(stCompany, "TermsAndConditions"); err != nil {
			return fmt.Errorf("add tenant_company_configs.terms_and_conditions: %w", err)
		}
	}
	stQuotation := &v091Quotation{}
	if !mig.HasColumn(stQuotation, "ShowTermsConditions") {
		if err := mig.AddColumn(stQuotation, "ShowTermsConditions"); err != nil {
			return fmt.Errorf("add tenant_quotations.show_terms_conditions: %w", err)
		}
	}
	return nil
}
