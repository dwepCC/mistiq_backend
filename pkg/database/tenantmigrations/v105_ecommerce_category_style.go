package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v105EcommerceSettings struct {
	ID            uint   `gorm:"primaryKey"`
	CategoryStyle string `gorm:"column:category_style;size:20;default:'circles'"`
}

func (v105EcommerceSettings) TableName() string { return "tenant_ecommerce_settings" }

// V105EcommerceCategoryStyle agrega category_style ('circles' | 'pills') para que el tenant elija
// cómo se navega por categorías en el Catálogo Digital, en vez de un único estilo fijo.
type V105EcommerceCategoryStyle struct{}

func (V105EcommerceCategoryStyle) Version() int { return 105 }
func (V105EcommerceCategoryStyle) Name() string { return "ecommerce_category_style" }

func (V105EcommerceCategoryStyle) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v105EcommerceSettings{}) {
		return nil
	}
	if mig.HasColumn(&v105EcommerceSettings{}, "CategoryStyle") {
		return nil
	}
	if err := mig.AddColumn(&v105EcommerceSettings{}, "CategoryStyle"); err != nil {
		return fmt.Errorf("add tenant_ecommerce_settings.category_style: %w", err)
	}
	return nil
}
