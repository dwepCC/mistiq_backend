package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V072CompanyFiscalDomicile backfill ubigeo/dirección fiscal para emisión SUNAT.
type V072CompanyFiscalDomicile struct{}

func (V072CompanyFiscalDomicile) Version() int  { return 72 }
func (V072CompanyFiscalDomicile) Name() string { return "company_fiscal_domicile" }

func (V072CompanyFiscalDomicile) Up(db *gorm.DB) error {
	if err := database.EnsureCompanyFiscalDomicile(db); err != nil {
		return fmt.Errorf("company fiscal domicile: %w", err)
	}
	return nil
}
