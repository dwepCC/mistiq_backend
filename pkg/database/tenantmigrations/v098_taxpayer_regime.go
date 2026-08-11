package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v098CompanyConfig struct {
	TaxpayerRegime string `gorm:"column:taxpayer_regime;size:20;default:'general'"`
}

func (v098CompanyConfig) TableName() string { return "tenant_company_configs" }

// V098TaxpayerRegime agrega el régimen tributario del contribuyente (general | nrus).
// No-breaking: default 'general' deja a los tenants existentes con el comportamiento actual.
type V098TaxpayerRegime struct{}

func (V098TaxpayerRegime) Version() int { return 98 }
func (V098TaxpayerRegime) Name() string { return "taxpayer_regime" }

func (V098TaxpayerRegime) Up(db *gorm.DB) error {
	mig := db.Migrator()
	st := &v098CompanyConfig{}
	if !mig.HasColumn(st, "TaxpayerRegime") {
		if err := mig.AddColumn(st, "TaxpayerRegime"); err != nil {
			return fmt.Errorf("add tenant_company_configs.taxpayer_regime: %w", err)
		}
	}
	return nil
}
