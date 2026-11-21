package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v168CompanyConfig struct {
	ShowIgvBreakdownOnSaleNote bool `gorm:"column:show_igv_breakdown_on_sale_note;default:true"`
}

func (v168CompanyConfig) TableName() string { return "tenant_company_configs" }

// V168CompanyShowIgvBreakdownOnSaleNote ajuste solo para nota de venta: discriminar o no Op.
// gravadas / IGV en el impreso (ticket, A4 y ESC/POS). Default true = comportamiento previo.
type V168CompanyShowIgvBreakdownOnSaleNote struct{}

func (V168CompanyShowIgvBreakdownOnSaleNote) Version() int { return 168 }
func (V168CompanyShowIgvBreakdownOnSaleNote) Name() string {
	return "company_show_igv_breakdown_on_sale_note"
}

func (V168CompanyShowIgvBreakdownOnSaleNote) Up(db *gorm.DB) error {
	mig := db.Migrator()
	st := &v168CompanyConfig{}
	if !mig.HasColumn(st, "ShowIgvBreakdownOnSaleNote") {
		if err := mig.AddColumn(st, "ShowIgvBreakdownOnSaleNote"); err != nil {
			return fmt.Errorf("add tenant_company_configs.show_igv_breakdown_on_sale_note: %w", err)
		}
	}
	return nil
}
