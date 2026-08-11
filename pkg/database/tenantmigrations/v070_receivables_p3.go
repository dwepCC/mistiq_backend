package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V070ReceivablesP3 confirmación BN en detracción y método crédito (CxC).
type V070ReceivablesP3 struct{}

func (V070ReceivablesP3) Version() int { return 70 }
func (V070ReceivablesP3) Name() string { return "receivables_p3" }

func (V070ReceivablesP3) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantSaleDetraccion{}); err != nil {
		return fmt.Errorf("tenant sale detraccion bn fields: %w", err)
	}
	if err := db.Model(&database.TenantSaleDetraccion{}).
		Where("bn_confirmation_status = '' OR bn_confirmation_status IS NULL").
		Update("bn_confirmation_status", "pending").Error; err != nil {
		return fmt.Errorf("backfill bn status: %w", err)
	}
	if err := database.EnsureCreditPaymentCondition(db); err != nil {
		return fmt.Errorf("credit payment condition: %w", err)
	}
	return nil
}
