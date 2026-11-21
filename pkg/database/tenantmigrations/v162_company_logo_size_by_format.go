package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v162CompanyConfig struct {
	LogoSizeTicket string `gorm:"column:logo_size_ticket;size:10;default:'mediano'"`
	LogoSizeA4     string `gorm:"column:logo_size_a4;size:10;default:'mediano'"`
}

func (v162CompanyConfig) TableName() string { return "tenant_company_configs" }

// V162CompanyLogoSizeByFormat tamaño del logo en TODOS los comprobantes, separado por
// formato (ticket vs A4): las proporciones de rollo térmico y hoja A4 son muy distintas.
type V162CompanyLogoSizeByFormat struct{}

func (V162CompanyLogoSizeByFormat) Version() int { return 162 }
func (V162CompanyLogoSizeByFormat) Name() string { return "company_logo_size_by_format" }

func (V162CompanyLogoSizeByFormat) Up(db *gorm.DB) error {
	mig := db.Migrator()
	st := &v162CompanyConfig{}
	if !mig.HasColumn(st, "LogoSizeTicket") {
		if err := mig.AddColumn(st, "LogoSizeTicket"); err != nil {
			return fmt.Errorf("add tenant_company_configs.logo_size_ticket: %w", err)
		}
	}
	if !mig.HasColumn(st, "LogoSizeA4") {
		if err := mig.AddColumn(st, "LogoSizeA4"); err != nil {
			return fmt.Errorf("add tenant_company_configs.logo_size_a4: %w", err)
		}
	}
	return nil
}
