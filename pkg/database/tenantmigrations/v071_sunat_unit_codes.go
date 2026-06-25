package tenantmigrations

import (
	"fmt"
	"strings"

	"tukifac/pkg/sunat"

	"gorm.io/gorm"
)

// V071SunatUnitCodes corrige unidades comerciales (ej. LT) a códigos catálogo SUNAT 03.
type V071SunatUnitCodes struct{}

func (V071SunatUnitCodes) Version() int  { return 71 }
func (V071SunatUnitCodes) Name() string { return "sunat_unit_codes" }

func (V071SunatUnitCodes) Up(db *gorm.DB) error {
	corrections := sunat.UnitCorrections()
	for from, to := range corrections {
		fromU := strings.ToUpper(strings.TrimSpace(from))
		toU := strings.ToUpper(strings.TrimSpace(to))
		if fromU == "" || fromU == toU {
			continue
		}
		if err := db.Exec(
			`UPDATE tenant_products SET unit = ? WHERE UPPER(TRIM(unit)) = ?`,
			toU, fromU,
		).Error; err != nil {
			return fmt.Errorf("tenant_products unit %s→%s: %w", fromU, toU, err)
		}
		if err := db.Exec(
			`UPDATE tenant_sale_items SET unit = ? WHERE UPPER(TRIM(unit)) = ?`,
			toU, fromU,
		).Error; err != nil {
			return fmt.Errorf("tenant_sale_items unit %s→%s: %w", fromU, toU, err)
		}
	}
	return nil
}
