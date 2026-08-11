package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v034RestaurantSetting struct {
	DeletionPin string `gorm:"size:72"`
}

func (v034RestaurantSetting) TableName() string { return "tenant_restaurant_settings" }

// V034RestaurantSettingsDeletionPin amplía deletion_pin para almacenar hash bcrypt (~60 chars).
type V034RestaurantSettingsDeletionPin struct{}

func (V034RestaurantSettingsDeletionPin) Version() int { return 34 }
func (V034RestaurantSettingsDeletionPin) Name() string { return "restaurant_settings_deletion_pin" }

func (V034RestaurantSettingsDeletionPin) Up(db *gorm.DB) error {
	mig := db.Migrator()
	cfg := &v034RestaurantSetting{}
	if !mig.HasTable(cfg) {
		return nil
	}
	if !mig.HasColumn(cfg, "DeletionPin") {
		return nil
	}
	if err := mig.AlterColumn(cfg, "DeletionPin"); err != nil {
		return fmt.Errorf("tenant_restaurant_settings.deletion_pin: %w", err)
	}
	return nil
}
