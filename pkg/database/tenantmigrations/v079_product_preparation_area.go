package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v079Product struct {
	ID              uint   `gorm:"primaryKey"`
	PreparationArea string `gorm:"column:preparation_area;size:50"`
}

func (v079Product) TableName() string { return "tenant_products" }

// V079ProductPreparationArea agrega preparation_area a tenant_products (idempotente).
// Auditoría V079: única columna del modelo TenantProduct sin migración incremental previa
// (branch_id → V061; resto en baseline V001 para tenants nuevos).
type V079ProductPreparationArea struct{}

func (V079ProductPreparationArea) Version() int { return 79 }
func (V079ProductPreparationArea) Name() string { return "product_preparation_area" }

func (V079ProductPreparationArea) Up(db *gorm.DB) error {
	mig := db.Migrator()
	p := &v079Product{}
	if !mig.HasTable(p) {
		return nil
	}
	if !mig.HasColumn(p, "PreparationArea") {
		if err := mig.AddColumn(p, "PreparationArea"); err != nil {
			return fmt.Errorf("add tenant_products.preparation_area: %w", err)
		}
	}
	return nil
}
