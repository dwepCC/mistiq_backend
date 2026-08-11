package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v044DeliveryCompany struct {
	ID        uint   `gorm:"primaryKey"`
	Name      string `gorm:"size:100;not null"`
	SortOrder int    `gorm:"default:0"`
	Active    bool   `gorm:"default:true"`
}

func (v044DeliveryCompany) TableName() string { return "tenant_delivery_companies" }

type v044DeliveryDriver struct {
	ID                uint  `gorm:"primaryKey"`
	DeliveryCompanyID *uint `gorm:"index"`
}

func (v044DeliveryDriver) TableName() string { return "tenant_delivery_drivers" }

// V044DeliveryCompanies tabla de empresas delivery y vínculo en repartidores.
type V044DeliveryCompanies struct{}

func (V044DeliveryCompanies) Version() int { return 44 }
func (V044DeliveryCompanies) Name() string { return "delivery_companies" }

func (V044DeliveryCompanies) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&v044DeliveryCompany{}); err != nil {
		return fmt.Errorf("tenant_delivery_companies: %w", err)
	}
	if !db.Migrator().HasColumn("tenant_delivery_drivers", "delivery_company_id") {
		if err := db.Migrator().AddColumn(&v044DeliveryDriver{}, "DeliveryCompanyID"); err != nil {
			return fmt.Errorf("tenant_delivery_drivers.delivery_company_id: %w", err)
		}
	}
	return nil
}
