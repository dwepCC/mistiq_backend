package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v113PrepaymentApplication struct {
	ReversedAt *time.Time `gorm:"column:reversed_at"`
}

func (v113PrepaymentApplication) TableName() string { return "tenant_sale_prepayment_applications" }

// V113PrepaymentApplicationReversedAt marca una aplicación de anticipo como revertida (sin borrarla)
// cuando la venta que dedujo se anula por nota de crédito.
//
// Antes, anular con NC la venta que dedujo un anticipo (SaleService.Cancel) revertía stock, series y
// caja, pero nunca tocaba tenant_sale_prepayment_applications ni el balance_amount del voucher
// origen: la factura de anticipo quedaba con saldo reducido para siempre y nunca volvía a aparecer en
// la lista de anticipos disponibles. reversed_at (NULL = aplicación vigente) permite reponer el saldo
// del voucher y a la vez conservar el rastro de que esa deducción existió y fue revertida.
type V113PrepaymentApplicationReversedAt struct{}

func (V113PrepaymentApplicationReversedAt) Version() int { return 113 }
func (V113PrepaymentApplicationReversedAt) Name() string { return "prepayment_application_reversed_at" }

func (V113PrepaymentApplicationReversedAt) Up(db *gorm.DB) error {
	mig := db.Migrator()
	row := &v113PrepaymentApplication{}
	if !mig.HasColumn(row, "ReversedAt") {
		if err := mig.AddColumn(row, "ReversedAt"); err != nil {
			return fmt.Errorf("add tenant_sale_prepayment_applications.reversed_at: %w", err)
		}
	}
	return nil
}
