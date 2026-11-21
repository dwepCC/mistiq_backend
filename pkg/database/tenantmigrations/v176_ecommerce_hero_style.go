package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v176EcommerceSettings struct {
	ID        uint   `gorm:"primaryKey"`
	HeroStyle string `gorm:"column:hero_style;size:20;default:'split'"`
}

func (v176EcommerceSettings) TableName() string { return "tenant_ecommerce_settings" }

// V176EcommerceHeroStyle agrega hero_style ('split' | 'image') para que el tenant elija si el banner
// principal de la tienda muestra texto + botón junto a la imagen, o solo la imagen completa. Las filas
// existentes quedan en 'split' (el diseño de siempre). Idempotente.
type V176EcommerceHeroStyle struct{}

func (V176EcommerceHeroStyle) Version() int { return 176 }
func (V176EcommerceHeroStyle) Name() string { return "ecommerce_hero_style" }

func (V176EcommerceHeroStyle) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v176EcommerceSettings{}) {
		return nil
	}
	if mig.HasColumn(&v176EcommerceSettings{}, "HeroStyle") {
		return nil
	}
	if err := mig.AddColumn(&v176EcommerceSettings{}, "HeroStyle"); err != nil {
		return fmt.Errorf("add tenant_ecommerce_settings.hero_style: %w", err)
	}
	return nil
}
