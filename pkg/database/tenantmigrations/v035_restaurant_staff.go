package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v035Staff struct {
	ID             uint   `gorm:"primaryKey"`
	UserID         uint   `gorm:"uniqueIndex;not null"`
	EmployeeType   string `gorm:"size:30;not null;index"`
	StaffCode      string `gorm:"size:20;index"`
	PinHash        string `gorm:"size:72"`
	DisplayName    string `gorm:"size:100"`
	IsActive       bool   `gorm:"default:true"`
	CanCharge      bool   `gorm:"default:false"`
	CanDiscount    bool   `gorm:"default:false"`
	CanOpenTable   bool   `gorm:"default:true"`
	KitchenAccess  bool   `gorm:"default:false"`
	DeliveryAccess bool   `gorm:"default:false"`
	LegacyWaiterID *uint  `gorm:"index"`
	Notes          string `gorm:"type:text"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (v035Staff) TableName() string { return "tenant_restaurant_staff" }

type v035Settings struct {
	StaffV2Enabled   bool `gorm:"default:false"`
	PermCacheVersion uint `gorm:"default:0"`
}

func (v035Settings) TableName() string { return "tenant_restaurant_settings" }

// V035RestaurantStaff perfil operativo + flags staff v2.
type V035RestaurantStaff struct{}

func (V035RestaurantStaff) Version() int { return 35 }
func (V035RestaurantStaff) Name() string { return "restaurant_staff_v2" }

func (V035RestaurantStaff) Up(db *gorm.DB) error {
	mig := db.Migrator()
	staff := &v035Staff{}
	if !mig.HasTable(staff) {
		if err := mig.CreateTable(staff); err != nil {
			return fmt.Errorf("tenant_restaurant_staff: %w", err)
		}
	}
	cfg := &v035Settings{}
	if mig.HasTable(cfg) {
		if !mig.HasColumn(cfg, "StaffV2Enabled") {
			if err := mig.AddColumn(cfg, "StaffV2Enabled"); err != nil {
				return fmt.Errorf("tenant_restaurant_settings.staff_v2_enabled: %w", err)
			}
		}
		if !mig.HasColumn(cfg, "PermCacheVersion") {
			if err := mig.AddColumn(cfg, "PermCacheVersion"); err != nil {
				return fmt.Errorf("tenant_restaurant_settings.perm_cache_version: %w", err)
			}
		}
	}
	return backfillStaffFromLegacyRoles(db)
}

func backfillStaffFromLegacyRoles(db *gorm.DB) error {
	if !db.Migrator().HasTable(&v035Staff{}) {
		return nil
	}
	type legacyRole struct {
		UserID uint
		Role   string
	}
	var roles []legacyRole
	if err := db.Table("tenant_user_restaurant_roles").Find(&roles).Error; err != nil {
		return nil
	}
	roleToType := map[string]string{
		"admin": "admin", "vendedor": "cashier", "mozo": "waiter", "cocinero": "cook",
	}
	for _, r := range roles {
		et, ok := roleToType[r.Role]
		if !ok {
			continue
		}
		var n int64
		db.Model(&v035Staff{}).Where("user_id = ?", r.UserID).Count(&n)
		if n > 0 {
			continue
		}
		canCharge := et == "cashier" || et == "admin"
		kitchen := et == "cook" || et == "admin"
		delivery := et == "driver"
		row := v035Staff{
			UserID: r.UserID, EmployeeType: et, IsActive: true,
			CanCharge: canCharge, CanOpenTable: true, KitchenAccess: kitchen, DeliveryAccess: delivery,
		}
		if err := db.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}
