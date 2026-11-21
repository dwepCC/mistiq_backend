package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v161CompanyConfig struct {
	ShowBusinessNameOnSaleNote bool `gorm:"column:show_business_name_on_sale_note;default:true"`
}

func (v161CompanyConfig) TableName() string { return "tenant_company_configs" }

// V161CompanyShowBusinessNameOnSaleNote ajuste solo para nota de venta: mostrar u ocultar
// la razón social del emisor en el impreso (con fallback a nombre comercial y viceversa,
// resuelto en el frontend al armar el PDF).
type V161CompanyShowBusinessNameOnSaleNote struct{}

func (V161CompanyShowBusinessNameOnSaleNote) Version() int { return 161 }
func (V161CompanyShowBusinessNameOnSaleNote) Name() string {
	return "company_show_business_name_on_sale_note"
}

func (V161CompanyShowBusinessNameOnSaleNote) Up(db *gorm.DB) error {
	mig := db.Migrator()
	st := &v161CompanyConfig{}
	if !mig.HasColumn(st, "ShowBusinessNameOnSaleNote") {
		if err := mig.AddColumn(st, "ShowBusinessNameOnSaleNote"); err != nil {
			return fmt.Errorf("add tenant_company_configs.show_business_name_on_sale_note: %w", err)
		}
	}
	return nil
}
