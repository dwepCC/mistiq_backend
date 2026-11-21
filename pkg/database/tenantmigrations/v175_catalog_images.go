package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v175Category struct {
	ID       uint   `gorm:"primaryKey"`
	ImageURL string `gorm:"column:image_url;size:255"`
}

func (v175Category) TableName() string { return "tenant_categories" }

type v175ProductImage struct {
	ID        uint   `gorm:"primaryKey"`
	ProductID uint   `gorm:"not null;index"`
	URL       string `gorm:"size:255;not null"`
	SortOrder int    `gorm:"default:0"`
	CreatedAt time.Time
}

func (v175ProductImage) TableName() string { return "tenant_product_images" }

// V175CatalogImages agrega la imagen de cada categoría (tenant_categories.image_url, para mostrarla en la
// tienda virtual) y la galería de imágenes adicionales de los productos (tenant_product_images). Sin
// backfill; idempotente.
type V175CatalogImages struct{}

func (V175CatalogImages) Version() int { return 175 }
func (V175CatalogImages) Name() string { return "catalog_images" }

func (V175CatalogImages) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if mig.HasTable(&v175Category{}) && !mig.HasColumn(&v175Category{}, "ImageURL") {
		if err := mig.AddColumn(&v175Category{}, "ImageURL"); err != nil {
			return fmt.Errorf("add tenant_categories.image_url: %w", err)
		}
	}
	if !mig.HasTable(&v175ProductImage{}) {
		if err := mig.CreateTable(&v175ProductImage{}); err != nil {
			return fmt.Errorf("create tenant_product_images: %w", err)
		}
	}
	return nil
}
