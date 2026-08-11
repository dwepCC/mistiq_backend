package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v094Product struct {
	ID            uint       `gorm:"primaryKey"`
	HasExpiryDate bool       `gorm:"column:has_expiry_date;default:false"`
	ExpiryDate    *time.Time `gorm:"column:expiry_date;type:date"`
}

func (v094Product) TableName() string { return "tenant_products" }

// V094ProductExpiryDate agrega control de vencimiento por producto.
type V094ProductExpiryDate struct{}

func (V094ProductExpiryDate) Version() int { return 94 }
func (V094ProductExpiryDate) Name() string { return "product_expiry_date" }

func (V094ProductExpiryDate) Up(db *gorm.DB) error {
	mig := db.Migrator()
	p := &v094Product{}
	if !mig.HasTable(p) {
		return nil
	}
	if !mig.HasColumn(p, "HasExpiryDate") {
		if err := mig.AddColumn(p, "HasExpiryDate"); err != nil {
			return fmt.Errorf("add tenant_products.has_expiry_date: %w", err)
		}
	}
	if !mig.HasColumn(p, "ExpiryDate") {
		if err := mig.AddColumn(p, "ExpiryDate"); err != nil {
			return fmt.Errorf("add tenant_products.expiry_date: %w", err)
		}
	}
	return nil
}
