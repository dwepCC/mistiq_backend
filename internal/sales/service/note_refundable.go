package service

import (
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/taxpayment"

	"gorm.io/gorm"
)

// Una nota de crédito acredita al cliente: si la factura NO está cobrada (venta a crédito, o cobrada en parte), esa parte baja la
// cuenta por cobrar y no hay dinero que devolver. Antes toda nota parcial dejaba una «devolución pendiente» por su total, aunque
// el cliente no hubiera pagado nada (caso AAR SERVICIOS, F001-123 a crédito con una nota de valor de S/ 90).
//
// NoteRefundableAmount devuelve cuánto de la nota hay que devolver en dinero: solo el exceso de lo cobrado sobre lo que el cliente
// aún debe tras las notas. Para una venta cobrada completa es el total de la nota (igual que antes). Va en orden: una nota nueva
// solo devuelve lo que no cubrieron las anteriores.
func NoteRefundableAmount(db *gorm.DB, nc *database.TenantSale) (float64, error) {
	if nc == nil || nc.OriginalSaleID == nil || *nc.OriginalSaleID == 0 {
		return money.RoundDisplay(nc.Total), nil // nota independiente: no hay venta local contra la cual medir
	}
	var orig database.TenantSale
	if err := db.First(&orig, *nc.OriginalSaleID).Error; err != nil {
		return money.RoundDisplay(nc.Total), nil
	}

	// Lo que el cliente debe pagar directo (sin detracción/SPOT) antes de las notas.
	target := orig.Total
	var det database.TenantSaleDetraccion
	if err := db.First(&det, "sale_id = ?", orig.ID).Error; err == nil {
		if taxpayment.IsSelfDetractionStatus(det.BnConfirmationStatus) {
			if det.InvoiceTotalPen > 0 {
				target = det.InvoiceTotalPen
			}
		} else {
			target = det.NetPayablePen
		}
	}

	// Lo cobrado: los pagos directos de la venta; si se emitió desde una nota de venta, los de esa nota (donde entró el dinero).
	paidSaleID := orig.ID
	if orig.IssuedFromNotaSaleID != nil && *orig.IssuedFromNotaSaleID > 0 {
		paidSaleID = *orig.IssuedFromNotaSaleID
	}
	var payments []database.TenantSalePayment
	if err := db.Where("sale_id IN ?", []uint{orig.ID, paidSaleID}).Find(&payments).Error; err != nil {
		return 0, err
	}
	var paid float64
	seen := map[uint]bool{}
	for _, p := range payments {
		if seen[p.ID] || taxpayment.IsDetractionCode(p.Method) || paymentcondition.IsCreditCode(p.Method) {
			continue
		}
		seen[p.ID] = true
		paid += p.Amount
	}

	// Notas anteriores aceptadas de la misma venta (las anteriores en orden de creación).
	var before float64
	if err := db.Model(&database.TenantSale{}).
		Where("original_sale_id = ? AND doc_type = ? AND billing_status = ? AND status <> ? AND id < ?", orig.ID, "NOTA_CREDITO", "accepted", "cancelled", nc.ID).
		Select("COALESCE(SUM(total), 0)").Scan(&before).Error; err != nil {
		return 0, err
	}

	overpaid := func(credited float64) float64 {
		due := target - credited
		if due < 0 {
			due = 0
		}
		if over := paid - due; over > 0 {
			return over
		}
		return 0
	}
	refund := overpaid(before+nc.Total) - overpaid(before)
	if refund > nc.Total {
		refund = nc.Total
	}
	if refund < money.PaymentTolerance {
		return 0, nil
	}
	return money.RoundDisplay(refund), nil
}
