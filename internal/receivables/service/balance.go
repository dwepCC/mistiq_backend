package service

import (
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/taxpayment"
)

const (
	BnStatusPending   = "pending"
	BnStatusConfirmed = "confirmed"
	BnStatusRejected  = "rejected"
)

// DirectPaid suma pagos directos (excluye SPOT y crédito interno).
func DirectPaid(payments []database.TenantSalePayment) float64 {
	var sum float64
	for _, p := range payments {
		if taxpayment.IsDetractionCode(p.Method) || paymentcondition.IsCreditCode(p.Method) {
			continue
		}
		sum += p.Amount
	}
	return sum
}

// SaleBalance calcula montos CxC directos y SPOT pendiente.
func SaleBalance(sale database.TenantSale, det *database.TenantSaleDetraccion, payments []database.TenantSalePayment) (
	directTarget, directPaid, directDue, spotAmount, spotPending float64,
	bnStatus string,
) {
	if det != nil {
		directTarget = det.NetPayablePen
		spotAmount = det.DetractionAmountPen
		bnStatus = det.BnConfirmationStatus
		if bnStatus == "" {
			bnStatus = BnStatusPending
		}
		if spotAmount > 0 && bnStatus == BnStatusPending {
			spotPending = spotAmount
		}
	} else {
		directTarget = sale.Total
	}
	directPaid = DirectPaid(payments)
	directDue = money.RoundDisplay(directTarget - directPaid)
	if directDue < money.PaymentTolerance {
		directDue = 0
	}
	return
}

// HasOpenReceivable indica si la venta tiene saldo directo o SPOT BN pendiente.
func HasOpenReceivable(sale database.TenantSale, det *database.TenantSaleDetraccion, payments []database.TenantSalePayment) bool {
	if sale.Status == "cancelled" {
		return false
	}
	_, _, directDue, _, spotPending, bnStatus := SaleBalance(sale, det, payments)
	if directDue >= money.PaymentTolerance {
		return true
	}
	if spotPending >= money.PaymentTolerance && bnStatus == BnStatusPending {
		return true
	}
	return false
}
