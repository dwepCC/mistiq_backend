package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v127EcommerceSettings struct {
	ID        uint `gorm:"primaryKey"`
	ShowStock bool `gorm:"column:show_stock"`
}

func (v127EcommerceSettings) TableName() string { return "tenant_ecommerce_settings" }

// V127EcommerceShowStock agrega tenant_ecommerce_settings.show_stock: permite ocultar el
// stock/disponibilidad de los productos en la tienda pública (Módulos → Tienda Virtual →
// General). Default true para que el comportamiento actual (badge "Agotado" visible) no cambie
// para nadie que no toque el ajuste explícitamente.
type V127EcommerceShowStock struct{}

func (V127EcommerceShowStock) Version() int { return 127 }
func (V127EcommerceShowStock) Name() string { return "ecommerce_show_stock" }

func (V127EcommerceShowStock) Up(db *gorm.DB) error {
	mig := db.Migrator()
	row := &v127EcommerceSettings{}
	if !mig.HasColumn(row, "ShowStock") {
		if err := mig.AddColumn(row, "ShowStock"); err != nil {
			return fmt.Errorf("add tenant_ecommerce_settings.show_stock: %w", err)
		}
	}
	// AddColumn con default en el tag ya deja show_stock=true en las filas existentes (MySQL
	// aplica el DEFAULT al agregar la columna); este UPDATE es solo un resguardo por si algún
	// driver la dejara en NULL/false.
	if err := db.Table("tenant_ecommerce_settings").
		Where("show_stock IS NULL OR show_stock = ?", false).
		Update("show_stock", true).Error; err != nil {
		return fmt.Errorf("v127 backfill show_stock: %w", err)
	}
	return nil
}
