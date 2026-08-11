package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V043FiscalSaleLinks vincula retención/percepción con venta fiscal (pipeline unificado).
type V043FiscalSaleLinks struct{}

func (V043FiscalSaleLinks) Version() int { return 43 }
func (V043FiscalSaleLinks) Name() string { return "fiscal_sale_links_retention_perception" }

type v043RetentionSaleID struct {
	SaleID *uint `gorm:"column:sale_id;index"`
}

func (v043RetentionSaleID) TableName() string { return "tenant_retentions" }

type v043PerceptionSaleID struct {
	SaleID *uint `gorm:"column:sale_id;index"`
}

func (v043PerceptionSaleID) TableName() string { return "tenant_perceptions" }

func (V043FiscalSaleLinks) Up(db *gorm.DB) error {
	if db.Migrator().HasTable("tenant_retentions") {
		st := &v043RetentionSaleID{}
		if !db.Migrator().HasColumn(st, "SaleID") {
			if err := db.Migrator().AddColumn(st, "SaleID"); err != nil {
				return fmt.Errorf("tenant_retentions.sale_id: %w", err)
			}
		}
	}
	if db.Migrator().HasTable("tenant_perceptions") {
		st := &v043PerceptionSaleID{}
		if !db.Migrator().HasColumn(st, "SaleID") {
			if err := db.Migrator().AddColumn(st, "SaleID"); err != nil {
				return fmt.Errorf("tenant_perceptions.sale_id: %w", err)
			}
		}
	}
	return nil
}
