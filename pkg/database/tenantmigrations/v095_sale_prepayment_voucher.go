package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V095SalePrepaymentVoucher tabla mínima para emisión de comprobantes de anticipo (Fase 0).
type V095SalePrepaymentVoucher struct{}

func (V095SalePrepaymentVoucher) Version() int { return 95 }
func (V095SalePrepaymentVoucher) Name() string { return "sale_prepayment_voucher" }

func (V095SalePrepaymentVoucher) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantSalePrepaymentVoucher{}); err != nil {
		return fmt.Errorf("tenant prepayment voucher migrate: %w", err)
	}
	return nil
}
