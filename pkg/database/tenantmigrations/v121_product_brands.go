package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type v121Product struct {
	ID      uint  `gorm:"primaryKey"`
	BrandID *uint `gorm:"column:brand_id;index"`
}

func (v121Product) TableName() string { return "tenant_products" }

// V121ProductBrands crea tenant_brands (mismo rol que tenant_categories, sin jerarquía) y agrega
// tenant_products.brand_id — hasta ahora el menú "Marcas" del panel era solo un placeholder sin
// tabla propia detrás.
type V121ProductBrands struct{}

func (V121ProductBrands) Version() int { return 121 }
func (V121ProductBrands) Name() string { return "product_brands" }

func (V121ProductBrands) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantBrand{}); err != nil {
		return fmt.Errorf("crear tenant_brands: %w", err)
	}
	mig := db.Migrator()
	p := &v121Product{}
	if !mig.HasColumn(p, "BrandID") {
		if err := mig.AddColumn(p, "BrandID"); err != nil {
			return fmt.Errorf("add tenant_products.brand_id: %w", err)
		}
	}
	return nil
}
