package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v033DeliveryDriver struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (v033DeliveryDriver) TableName() string { return "tenant_delivery_drivers" }

// V033DeliveryDriversTimestamps alinea tenant_delivery_drivers con TenantDeliveryDriver (timestamps + soft delete).
type V033DeliveryDriversTimestamps struct{}

func (V033DeliveryDriversTimestamps) Version() int { return 33 }
func (V033DeliveryDriversTimestamps) Name() string { return "delivery_drivers_timestamps" }

func (V033DeliveryDriversTimestamps) Up(db *gorm.DB) error {
	mig := db.Migrator()
	driver := &v033DeliveryDriver{}
	if !mig.HasTable(driver) {
		return nil
	}
	for _, field := range []string{"CreatedAt", "UpdatedAt", "DeletedAt"} {
		if !mig.HasColumn(driver, field) {
			if err := mig.AddColumn(driver, field); err != nil {
				return fmt.Errorf("tenant_delivery_drivers.%s: %w", field, err)
			}
		}
	}
	return nil
}
