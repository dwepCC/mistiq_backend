package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v104EcommerceSlider struct {
	ID         uint   `gorm:"primaryKey"`
	Title      string `gorm:"size:150"`
	Subtitle   string `gorm:"size:255"`
	ButtonText string `gorm:"size:60"`
}

func (v104EcommerceSlider) TableName() string { return "tenant_ecommerce_sliders" }

// V104EcommerceBannerText agrega texto e botón a los sliders del Catálogo Digital (título,
// subtítulo y texto de botón), para que los banners dejen de ser solo imágenes y muestren un
// mensaje + llamado a la acción, igual que las tiendas virtuales de referencia.
type V104EcommerceBannerText struct{}

func (V104EcommerceBannerText) Version() int { return 104 }
func (V104EcommerceBannerText) Name() string { return "ecommerce_banner_text" }

func (V104EcommerceBannerText) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v104EcommerceSlider{}) {
		return nil
	}
	for _, col := range []string{"Title", "Subtitle", "ButtonText"} {
		if mig.HasColumn(&v104EcommerceSlider{}, col) {
			continue
		}
		if err := mig.AddColumn(&v104EcommerceSlider{}, col); err != nil {
			return fmt.Errorf("add tenant_ecommerce_sliders.%s: %w", col, err)
		}
	}
	return nil
}
