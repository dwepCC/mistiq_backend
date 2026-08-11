package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V042DespatchSaleLink vincula guías con venta fiscal (pipeline unificado).
type V042DespatchSaleLink struct{}

func (V042DespatchSaleLink) Version() int { return 42 }
func (V042DespatchSaleLink) Name() string { return "despatch_sale_id" }

type v042DespatchSaleID struct {
	SaleID *uint `gorm:"column:sale_id;index"`
}

func (v042DespatchSaleID) TableName() string { return "tenant_despatches" }

func (V042DespatchSaleLink) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_despatches") {
		return nil
	}
	st := &v042DespatchSaleID{}
	if !db.Migrator().HasColumn(st, "SaleID") {
		if err := db.Migrator().AddColumn(st, "SaleID"); err != nil {
			return fmt.Errorf("tenant_despatches.sale_id: %w", err)
		}
	}
	return nil
}
