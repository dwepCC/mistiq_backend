package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"
	"tukifac/pkg/salescope"

	"gorm.io/gorm"
)

// V076SaleOrigin clasifica origen comercial de ventas y backfill desde issued_from_nota_sale_id.
type V076SaleOrigin struct{}

func (V076SaleOrigin) Version() int  { return 76 }
func (V076SaleOrigin) Name() string { return "sale_origin" }

func (V076SaleOrigin) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantSale{}); err != nil {
		return fmt.Errorf("tenant_sales sale_origin: %w", err)
	}
	if err := db.Model(&database.TenantSale{}).
		Where("issued_from_nota_sale_id IS NOT NULL AND issued_from_nota_sale_id > 0").
		Update("sale_origin", salescope.SaleOriginConvertedFromNota).Error; err != nil {
		return fmt.Errorf("backfill converted_from_nota: %w", err)
	}
	if err := db.Model(&database.TenantSale{}).
		Where("(sale_origin IS NULL OR TRIM(sale_origin) = '')").
		Where("(issued_from_nota_sale_id IS NULL OR issued_from_nota_sale_id = 0)").
		Update("sale_origin", salescope.SaleOriginDirect).Error; err != nil {
		return fmt.Errorf("backfill direct: %w", err)
	}
	if err := db.Model(&database.TenantSale{}).
		Where("sale_origin IS NULL OR TRIM(sale_origin) = ''").
		Update("sale_origin", salescope.SaleOriginLegacy).Error; err != nil {
		return fmt.Errorf("backfill legacy: %w", err)
	}
	return nil
}
