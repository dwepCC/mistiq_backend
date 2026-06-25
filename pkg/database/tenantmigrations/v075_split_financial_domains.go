package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V075SplitFinancialDomains separa métodos, condiciones y conceptos tributarios en tablas propias.
type V075SplitFinancialDomains struct{}

func (V075SplitFinancialDomains) Version() int  { return 75 }
func (V075SplitFinancialDomains) Name() string { return "split_financial_domains" }

func (V075SplitFinancialDomains) Up(db *gorm.DB) error {
	if err := database.SplitFinancialDomainsFromLegacy(db); err != nil {
		return fmt.Errorf("split financial domains: %w", err)
	}
	return nil
}
