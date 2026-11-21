package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V174PrepaymentVoidCancelledSources marca como "voided" (y saldo 0) los anticipos cuya venta ya está
// anulada. Hasta ahora anular un anticipo no tocaba su voucher: seguía "open" con saldo y aparecía en la
// lista de anticipos a deducir. Idempotente y sin efecto si no hay filas.
type V174PrepaymentVoidCancelledSources struct{}

func (V174PrepaymentVoidCancelledSources) Version() int { return 174 }
func (V174PrepaymentVoidCancelledSources) Name() string {
	return "prepayment_void_cancelled_sources"
}

func (V174PrepaymentVoidCancelledSources) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable("tenant_sale_prepayment_vouchers") || !mig.HasTable("tenant_sales") {
		return nil
	}
	err := db.Exec(`UPDATE tenant_sale_prepayment_vouchers
SET status = 'voided', balance_amount = 0
WHERE status <> 'voided'
  AND sale_id IN (SELECT id FROM tenant_sales WHERE status = 'cancelled')`).Error
	if err != nil {
		return fmt.Errorf("backfill anticipos anulados: %w", err)
	}
	return nil
}
