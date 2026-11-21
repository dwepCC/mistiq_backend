package service

import (
	"errors"
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/money"

	"gorm.io/gorm"
)

// NoteItemSelection un ítem de la venta original y la cantidad que la nota devuelve/ajusta.
// La cantidad puede ser menor a la vendida (devolución/descuento parcial de esa línea).
type NoteItemSelection struct {
	OriginalItemID uint    `json:"original_item_id"`
	Quantity       float64 `json:"quantity"`
	// Amount: importe (con IGV) a acreditar de esta línea — nota por monto (ver note_amount.go).
	// Si viene > 0 se ignora Quantity y la nota no devuelve cantidades ni repone stock.
	Amount float64 `json:"amount,omitempty"`
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

// sumReturnedQuantitiesByOriginalItem suma, por línea original (OriginalSaleItemID), la cantidad
// COMERCIAL ya devuelta en notas de crédito/débito previas — Fase 7G, validación acumulada.
// Excluye notas "rejected" (una nota rechazada por SUNAT nunca llegó a repartir stock ni a tener
// validez legal, así que no debe consumir el cupo de la línea original); sí cuenta las "pending"
// (aún no confirmadas por SUNAT) porque son las que justamente hay que bloquear si ya alcanzan el
// límite — de lo contrario dos notas "pending" simultáneas podrían sumar más de lo vendido antes
// de que cualquiera de las dos sea aceptada.
func sumReturnedQuantitiesByOriginalItem(tx *gorm.DB, originalItemIDs []uint) (map[uint]float64, error) {
	result := make(map[uint]float64, len(originalItemIDs))
	if len(originalItemIDs) == 0 {
		return result, nil
	}
	type row struct {
		OriginalSaleItemID uint
		Total              float64
	}
	var rows []row
	err := tx.Table("tenant_sale_items AS tsi").
		Select("tsi.original_sale_item_id AS original_sale_item_id, SUM(tsi.quantity) AS total").
		Joins("JOIN tenant_sales ts ON ts.id = tsi.sale_id").
		Where("tsi.original_sale_item_id IN ? AND ts.billing_status <> ?", originalItemIDs, "rejected").
		Group("tsi.original_sale_item_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("calcular cantidad ya devuelta: %w", err)
	}
	for _, r := range rows {
		result[r.OriginalSaleItemID] = r.Total
	}
	return result, nil
}

// buildFullNoteItems arma las filas de tenant_sale_items de una nota de crédito "de todo" (copia
// el 100% de la venta original, sin selección de ítems/cantidades) — extraído como función pura
// para poder probarlo sin depender de la transacción/lock de CreateCreditNoteAndVoidSale. Copia
// tanto PresentationID como SaleUnitID (Fase 7G: antes de esta corrección, la nota completa
// copiaba SaleUnitID pero no PresentationID — asimetría frente a buildPartialNoteItems, que sí
// copiaba ambos).
func buildFullNoteItems(noteSaleID uint, origItems []database.TenantSaleItem) []database.TenantSaleItem {
	items := make([]database.TenantSaleItem, len(origItems))
	for i, it := range origItems {
		items[i] = database.TenantSaleItem{
			SaleID:             noteSaleID,
			ProductID:          it.ProductID,
			PresentationID:     it.PresentationID,
			SaleUnitID:         it.SaleUnitID,
			Code:               it.Code,
			Description:        it.Description,
			Unit:               it.Unit,
			Quantity:           it.Quantity,
			UnitPrice:          it.UnitPrice,
			Discount:           it.Discount,
			TaxRate:            it.TaxRate,
			IgvAffectationType: it.IgvAffectationType,
			Subtotal:           it.Subtotal,
			TaxAmount:          it.TaxAmount,
			Total:              it.Total,
			PackLabel:          it.PackLabel,
		}
	}
	return items
}

// buildPartialNoteItems valida las líneas elegidas contra la venta original — incluyendo lo que
// ya se devolvió antes en otras notas (alreadyReturned, ver sumReturnedQuantitiesByOriginalItem;
// nil o vacío = no había nada previo) — y arma tanto las filas de tenant_sale_items de la nota
// (proporcionales a la cantidad elegida, con OriginalSaleItemID para poder revertir el stock
// exacto después) como los totales de la nota — no se reutilizan orig.Subtotal/TaxAmount/Total
// como en la nota "de todo".
func buildPartialNoteItems(originalSaleID uint, origItems []database.TenantSaleItem, selections []NoteItemSelection, alreadyReturned map[uint]float64) ([]database.TenantSaleItem, float64, float64, float64, error) {
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
		already := alreadyReturned[sel.OriginalItemID]
		available := orig.Quantity - already
		if available < 0 {
			available = 0
		}
		if sel.Quantity > available+0.0001 {
			return nil, 0, 0, 0, fmt.Errorf(
				"cantidad inválida para %q: ya se devolvieron %.3f de %.3f vendidas, disponible %.3f",
				orig.Description, already, orig.Quantity, available,
			)
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
			SaleUnitID:             orig.SaleUnitID,
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
			PackLabel:              orig.PackLabel,
		})
		subtotal = money.RoundSunat(subtotal + itSub)
		taxAmount = money.RoundSunat(taxAmount + itTax)
		total = money.RoundSunat(total + itTot)
	}
	return items, subtotal, taxAmount, total, nil
}
