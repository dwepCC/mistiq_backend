package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V068SaleDetraccion cuenta BN en empresa y tabla tenant_sale_detraccion.
type V068SaleDetraccion struct{}

func (V068SaleDetraccion) Version() int  { return 68 }
func (V068SaleDetraccion) Name() string { return "sale_detraccion" }

func (V068SaleDetraccion) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantCompanyConfig{}, &database.TenantSaleDetraccion{}); err != nil {
		return fmt.Errorf("tenant detraccion migrate: %w", err)
	}
	return nil
}
