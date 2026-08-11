package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V096PrepaymentVoucherDefinitive campos definitivos e índices para vouchers de anticipo.
type V096PrepaymentVoucherDefinitive struct{}

func (V096PrepaymentVoucherDefinitive) Version() int { return 96 }
func (V096PrepaymentVoucherDefinitive) Name() string { return "prepayment_voucher_definitive" }

func (V096PrepaymentVoucherDefinitive) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantSalePrepaymentVoucher{}); err != nil {
		return fmt.Errorf("tenant prepayment voucher v96 migrate: %w", err)
	}
	return nil
}
