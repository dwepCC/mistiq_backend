package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v053DeliveryCompany struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (v053DeliveryCompany) TableName() string { return "tenant_delivery_companies" }

// V053DeliveryCompaniesTimestamps alinea tenant_delivery_companies con TenantDeliveryCompany (timestamps + soft delete).
// v044 creó la tabla sin deleted_at; GORM filtra deleted_at IS NULL y falla en tenants ya migrados.
type V053DeliveryCompaniesTimestamps struct{}

func (V053DeliveryCompaniesTimestamps) Version() int { return 53 }
func (V053DeliveryCompaniesTimestamps) Name() string { return "delivery_companies_timestamps" }

func (V053DeliveryCompaniesTimestamps) Up(db *gorm.DB) error {
	mig := db.Migrator()
	company := &v053DeliveryCompany{}
	if !mig.HasTable(company) {
		return nil
	}
	for _, field := range []string{"CreatedAt", "UpdatedAt", "DeletedAt"} {
		if !mig.HasColumn(company, field) {
			if err := mig.AddColumn(company, field); err != nil {
				return fmt.Errorf("tenant_delivery_companies.%s: %w", field, err)
			}
		}
	}
	return nil
}
