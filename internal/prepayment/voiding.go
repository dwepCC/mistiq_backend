package prepayment

import (
	"fmt"
	"strings"

	"tukifac/pkg/database"
	sunatpre "tukifac/pkg/sunat/prepayment"

	"gorm.io/gorm"
)

// ActiveConsumerNumbers devuelve los números de los comprobantes vigentes (no anulados) que dedujeron
// este anticipo. Las aplicaciones cuyo comprobante ya fue anulado no cuentan aunque no se hayan marcado
// como revertidas (datos anteriores a la reversión automática).
func ActiveConsumerNumbers(db *gorm.DB, sourceSaleID uint) ([]string, error) {
	var numbers []string
	err := db.Table("tenant_sale_prepayment_applications AS a").
		Joins("JOIN tenant_sales c ON c.id = a.consumer_sale_id").
		Where("a.source_sale_id = ? AND a.reversed_at IS NULL AND c.status <> ?", sourceSaleID, "cancelled").
		Order("a.id ASC").
		Pluck("c.number", &numbers).Error
	return numbers, err
}

// EnsureVoucherVoidable falla si la venta es un anticipo que ya fue deducido en comprobantes vigentes:
// anularlo dejaría esos comprobantes apuntando a un anticipo inexistente ante SUNAT. Hay que anular
// primero esos comprobantes (lo que repone el saldo) y recién después el anticipo.
// Si la venta no es un anticipo, no hace nada.
func EnsureVoucherVoidable(db *gorm.DB, saleID uint) error {
	var n int64
	if err := db.Model(&database.TenantSalePrepaymentVoucher{}).Where("sale_id = ?", saleID).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	nums, err := ActiveConsumerNumbers(db, saleID)
	if err != nil {
		return err
	}
	if len(nums) > 0 {
		return fmt.Errorf("no se puede anular este anticipo porque ya fue deducido en: %s. Anule primero esos comprobantes", strings.Join(nums, ", "))
	}
	return nil
}

// VoidSourceVoucherTx marca como anulado el voucher de la venta (si es un anticipo) y pone su saldo en
// cero, para que deje de ofrecerse como deducible. Idempotente. Se llama al anular la venta origen
// (nota de crédito aceptada, rechazo SUNAT, anulación interna).
func (s *Service) VoidSourceVoucherTx(tx *gorm.DB, saleID uint) error {
	return tx.Model(&database.TenantSalePrepaymentVoucher{}).
		Where("sale_id = ? AND status <> ?", saleID, sunatpre.StatusVoided).
		Updates(map[string]interface{}{
			"status":         sunatpre.StatusVoided,
			"balance_amount": 0,
		}).Error
}

// sourceSaleCancelled indica si la venta del anticipo está anulada.
func sourceSaleCancelled(db *gorm.DB, saleID uint) bool {
	var st string
	if db.Model(&database.TenantSale{}).Select("status").Where("id = ?", saleID).Scan(&st).Error != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(st), "cancelled")
}
