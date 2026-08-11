package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v032Session struct {
	OrderCode         string `gorm:"size:32;index"`
	OrderType         string `gorm:"size:20;default:dine_in;index"`
	OrderStatus       string `gorm:"size:30;default:pending;index"`
	ContactID         *uint  `gorm:"index"`
	CustomerName      string `gorm:"size:200"`
	CustomerPhone     string `gorm:"size:30"`
	DeliveryDriverID  *uint  `gorm:"index"`
	DeliveryAddress   string `gorm:"type:text"`
	DeliveryReference string `gorm:"size:255"`
	EstimatedMinutes  int    `gorm:"default:0"`
	SentToKitchenAt   *time.Time
	ReadyAt           *time.Time
	PaidAt            *time.Time
}

func (v032Session) TableName() string { return "tenant_table_sessions" }

type v032Sale struct {
	RestaurantSessionID *uint `gorm:"index"`
}

func (v032Sale) TableName() string { return "tenant_sales" }

type v032DeliveryDriver struct {
	ID          uint   `gorm:"primaryKey"`
	Name        string `gorm:"size:100;not null"`
	Phone       string `gorm:"size:30"`
	VehicleType string `gorm:"size:50"`
	Plate       string `gorm:"size:20"`
	Active      bool   `gorm:"default:true"`
	Notes       string `gorm:"type:text"`
}

func (v032DeliveryDriver) TableName() string { return "tenant_delivery_drivers" }

// V032RestaurantOrders pedidos restaurante: tipo, estado, delivery, vínculo venta.
type V032RestaurantOrders struct{}

func (V032RestaurantOrders) Version() int { return 32 }
func (V032RestaurantOrders) Name() string { return "restaurant_orders" }

func (V032RestaurantOrders) Up(db *gorm.DB) error {
	mig := db.Migrator()

	sess := &v032Session{}
	if mig.HasTable(sess) {
		fields := []string{
			"OrderCode", "OrderType", "OrderStatus", "ContactID", "CustomerName", "CustomerPhone",
			"DeliveryDriverID", "DeliveryAddress", "DeliveryReference", "EstimatedMinutes",
			"SentToKitchenAt", "ReadyAt", "PaidAt",
		}
		for _, f := range fields {
			if !mig.HasColumn(sess, f) {
				if err := mig.AddColumn(sess, f); err != nil {
					return fmt.Errorf("tenant_table_sessions.%s: %w", f, err)
				}
			}
		}
	}

	driver := &v032DeliveryDriver{}
	if !mig.HasTable(driver) {
		if err := mig.CreateTable(driver); err != nil {
			return fmt.Errorf("tenant_delivery_drivers: %w", err)
		}
	}

	sale := &v032Sale{}
	if mig.HasTable(sale) && !mig.HasColumn(sale, "RestaurantSessionID") {
		if err := mig.AddColumn(sale, "RestaurantSessionID"); err != nil {
			return fmt.Errorf("tenant_sales.restaurant_session_id: %w", err)
		}
	}

	return nil
}
