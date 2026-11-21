package service

import (
	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	"tukifac/pkg/money"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// Recargo al consumo en el comprobante electrónico (DL 25988, 5.ª disp. compl.). Es un cargo global del catálogo 53 (código 46) que NO afecta la
// base imponible del IGV: el valor de venta y el IGV quedan igual, el subtotal (TaxInclusiveAmount) no lo incluye y el importe total a pagar
// (PayableAmount) sí. En factura y boleta va como cac:AllowanceCharge + ChargeTotalAmount; la nota de crédito/débito solo lleva ChargeTotalAmount.

// applyServiceChargeToInvoicePayload agrega el cargo 46 a una factura/boleta. mtoImpVenta ya incluye el recargo (viene de sale.Total).
func applyServiceChargeToInvoicePayload(p *facturador.InvoicePayload, sale *database.TenantSale) {
	charge := round2(sale.ServiceChargeAmount)
	if charge <= 0 {
		return
	}
	p.Cargos = append(p.Cargos, chargeFromAmounts(tax.ServiceChargeCode, p.ValorVenta, charge))
	p.SumOtrosCargos = charge
	p.SubTotal = round2(p.MtoImpVenta - charge)
}

// applyServiceChargeToNotePayload reproduce en la nota el recargo de la venta que corrige (total) o su parte proporcional (parcial).
func applyServiceChargeToNotePayload(p *facturador.NotePayload, noteSale *database.TenantSale) {
	charge := round2(noteSale.ServiceChargeAmount)
	if charge <= 0 {
		return
	}
	p.SumOtrosCargos = charge
	p.SubTotal = round2(p.MtoImpVenta - charge)
}

// partialNoteServiceCharge recargo que devuelve una nota de crédito parcial: proporcional al valor de venta devuelto, sin pasarse de lo que aún
// no se acreditó en notas anteriores; la nota que devuelve lo último que quedaba se lleva el resto exacto (así la suma de las notas nunca
// difiere en céntimos del recargo cobrado).
func partialNoteServiceCharge(tx *gorm.DB, orig *database.TenantSale, origItems []database.TenantSaleItem, selections []NoteItemSelection, noteSubtotal float64) (float64, error) {
	if orig.ServiceChargeAmount <= 0 || orig.Subtotal <= 0 || noteSubtotal <= 0 {
		return 0, nil
	}
	var credited float64
	if err := tx.Model(&database.TenantSale{}).
		Where("original_sale_id = ? AND doc_type = ? AND status <> ? AND billing_status <> ?", orig.ID, "NOTA_CREDITO", "cancelled", "rejected").
		Select("COALESCE(SUM(service_charge_amount), 0)").Scan(&credited).Error; err != nil {
		return 0, err
	}
	remaining := round2(orig.ServiceChargeAmount - credited)
	if remaining <= 0 {
		return 0, nil
	}
	charge := money.RoundDisplay(orig.ServiceChargeAmount * noteSubtotal / orig.Subtotal)

	ids := make([]uint, len(origItems))
	selected := make(map[uint]float64, len(selections))
	for i, it := range origItems {
		ids[i] = it.ID
	}
	for _, sel := range selections {
		selected[sel.OriginalItemID] += sel.Quantity
	}
	returned, err := sumReturnedQuantitiesByOriginalItem(tx, ids)
	if err != nil {
		return 0, err
	}
	coversAll := true
	for _, it := range origItems {
		if it.Quantity-returned[it.ID]-selected[it.ID] > 0.0001 {
			coversAll = false
			break
		}
	}
	if coversAll || charge > remaining {
		charge = remaining
	}
	return charge, nil
}
