package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V066SaleFiscalContext tablas de información adicional fiscal por venta.
type V066SaleFiscalContext struct{}

func (V066SaleFiscalContext) Version() int  { return 66 }
func (V066SaleFiscalContext) Name() string { return "sale_fiscal_context" }

func (V066SaleFiscalContext) Up(db *gorm.DB) error {
	models := []interface{}{
		&database.TenantSaleFiscalProfile{},
		&database.TenantSaleFiscalReference{},
		&database.TenantSaleFiscalObligation{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			return fmt.Errorf("sale fiscal context migrate: %w", err)
		}
	}
	return nil
}
