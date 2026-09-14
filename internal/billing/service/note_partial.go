package service

import (
	"errors"
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/money"
)

// NoteItemSelection un ítem de la venta original y la cantidad que la nota devuelve/ajusta.
// La cantidad puede ser menor a la vendida (devolución/descuento parcial de esa línea).
type NoteItemSelection struct {
	OriginalItemID uint    `json:"original_item_id"`
	Quantity       float64 `json:"quantity"`
}

// partialCreditNoteReasonCodes motivos del catálogo SUNAT 09 que implican devolver o
// descontar bienes concretos — ahí tiene sentido elegir ítems y cantidades en vez de
// copiar el 100% de la venta. El resto (02/03 corrección de datos, 11/12/13 ajustes de
// exportación/IVAP/fecha de pago) no mueve mercadería: siguen copiando todo, como antes
// de esta fase.
var partialCreditNoteReasonCodes = map[string]bool{
	"04": true, // Descuento global
	"05": true, // Descuento por ítem
	"06": true, // Devolución total
	"07": true, // Devolución por ítem
	"08": true, // Bonificación
	"09": true, // Disminución en el valor
	"10": true, // Otros conceptos
}

// IsPartialCreditNoteReason expone el catálogo de motivos "parciales" al handler, para
// que el frontend sepa cuándo tiene sentido pedir ítems/cantidades.
func IsPartialCreditNoteReason(reasonCode string) bool {
	return partialCreditNoteReasonCodes[strings.TrimSpace(reasonCode)]
}

// buildPartialNoteItems valida las líneas elegidas contra la venta original y arma tanto
// las filas de tenant_sale_items de la nota (proporcionales a la cantidad elegida, con
// OriginalSaleItemID para poder revertir el stock exacto después) como los totales de la
// nota — no se reutilizan orig.Subtotal/TaxAmount/Total como en la nota "de todo".
func buildPartialNoteItems(originalSaleID uint, origItems []database.TenantSaleItem, selections []NoteItemSelection) ([]database.TenantSaleItem, float64, float64, float64, error) {
	if len(selections) == 0 {
		return nil, 0, 0, 0, errors.New("seleccione al menos un ítem para la nota parcial")
	}
	byID := make(map[uint]database.TenantSaleItem, len(origItems))
	for _, it := range origItems {
		byID[it.ID] = it
	}

	items := make([]database.TenantSaleItem, 0, len(selections))
	var subtotal, taxAmount, total float64
	seen := make(map[uint]bool, len(selections))
	for _, sel := range selections {
		if seen[sel.OriginalItemID] {
			return nil, 0, 0, 0, fmt.Errorf("el ítem %d está repetido en la selección", sel.OriginalItemID)
		}
		seen[sel.OriginalItemID] = true
		orig, ok := byID[sel.OriginalItemID]
		if !ok {
			return nil, 0, 0, 0, fmt.Errorf("el ítem %d no pertenece a la venta %d", sel.OriginalItemID, originalSaleID)
		}
		if sel.Quantity <= 0 {
			return nil, 0, 0, 0, fmt.Errorf("cantidad inválida para %q: debe ser mayor a cero", orig.Description)
		}
		if sel.Quantity > orig.Quantity+0.0001 {
			return nil, 0, 0, 0, fmt.Errorf("cantidad inválida para %q: máximo %.3f (lo vendido)", orig.Description, orig.Quantity)
		}
		ratio := sel.Quantity / orig.Quantity
		itSub := money.RoundSunat(orig.Subtotal * ratio)
		itTax := money.RoundSunat(orig.TaxAmount * ratio)
		itTot := money.RoundSunat(orig.Total * ratio)
		itDisc := money.RoundSunat(orig.Discount * ratio)
		itLineDisc := money.RoundSunat(orig.LineDiscountSubtotal * ratio)
		itGlobalDisc := money.RoundSunat(orig.GlobalDiscountSubtotal * ratio)
		origItemID := orig.ID
		items = append(items, database.TenantSaleItem{
			ProductID:              orig.ProductID,
			PresentationID:         orig.PresentationID,
			Code:                   orig.Code,
			Description:            orig.Description,
			Unit:                   orig.Unit,
			Quantity:               sel.Quantity,
			UnitPrice:              orig.UnitPrice,
			Discount:               itDisc,
			LineDiscountSubtotal:   itLineDisc,
			GlobalDiscountSubtotal: itGlobalDisc,
			TaxRate:                orig.TaxRate,
			IgvAffectationType:     orig.IgvAffectationType,
			Subtotal:               itSub,
			TaxAmount:              itTax,
			Total:                  itTot,
			OriginalSaleItemID:     &origItemID,
		})
		subtotal = money.RoundSunat(subtotal + itSub)
		taxAmount = money.RoundSunat(taxAmount + itTax)
		total = money.RoundSunat(total + itTot)
	}
	return items, subtotal, taxAmount, total, nil
}
