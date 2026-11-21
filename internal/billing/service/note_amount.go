package service

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// Nota de crédito POR MONTO.
//
// Hasta ahora una nota parcial solo sabía devolver cantidades: la nota era una fracción de la venta
// (cantidad elegida / cantidad vendida). Eso no sirve cuando el ajuste es un IMPORTE y no una devolución
// de mercadería: «la venta salió más barata, réstenle S/ 90», un descuento posterior, una rebaja
// pactada. Aquí la nota se arma a partir del importe (con IGV) que se quiere acreditar:
//
//   - Se reparte entre las líneas elegidas (o todas) proporcional al valor de cada línea, o se fija a
//     mano por línea (NoteItemSelection.Amount).
//   - Cada línea conserva su afectación (gravado/exonerado/inafecto…) y su tasa: la base y el IGV de la
//     nota salen de ese importe, no de una proporción de la cantidad.
//   - Es un ajuste de valor, NO una devolución: las líneas de la nota no apuntan a la línea original
//     (OriginalSaleItemID = NULL), así que no consumen el cupo de cantidad ni reponen stock.
//   - Como no hay cupo por cantidad, el tope es por importe: todo lo ya acreditado en notas no
//     rechazadas más esta nota no puede superar el valor de la venta.

// amountCreditNoteReasonCodes motivos del catálogo SUNAT 09 que ajustan el valor sin devolver bienes.
// 06/07/08 siguen siendo por cantidad (mueven mercadería); 01/02/03 copian la venta; 11/12/13 no cambian.
var amountCreditNoteReasonCodes = map[string]bool{
	"04": true, // Descuento global
	"05": true, // Descuento por ítem
	"09": true, // Disminución en el valor
	"10": true, // Otros conceptos
}

// IsAmountCreditNoteReason: true si el motivo admite acreditar un importe en vez de cantidades.
func IsAmountCreditNoteReason(reasonCode string) bool {
	return amountCreditNoteReasonCodes[strings.TrimSpace(reasonCode)]
}

// noStockReturnCreditNoteReasonCodes motivos cuyo efecto es de precio: el cliente se queda con la
// mercadería, así que aunque la nota traiga líneas con cantidad NO se repone stock.
var noStockReturnCreditNoteReasonCodes = map[string]bool{
	"04": true, "05": true, "09": true,
}

// CreditNoteReturnsStock: false cuando el motivo es un ajuste de precio (el stock no vuelve).
func CreditNoteReturnsStock(reasonCode string) bool {
	return !noStockReturnCreditNoteReasonCodes[strings.TrimSpace(reasonCode)]
}

// isAmountNote: la solicitud pide un importe (total repartido o por línea).
func isAmountNote(selections []NoteItemSelection, totalAmount float64) bool {
	if totalAmount > 0 {
		return true
	}
	for _, s := range selections {
		if s.Amount > 0 {
			return true
		}
	}
	return false
}

// creditedAmountOnSale suma lo ya acreditado por notas de crédito no rechazadas de la venta, SIN el
// recargo al consumo (que no forma parte del valor de las líneas y se acredita aparte).
func creditedAmountOnSale(tx *gorm.DB, saleID uint) (float64, error) {
	var credited float64
	err := tx.Model(&database.TenantSale{}).
		Where("original_sale_id = ? AND doc_type = ? AND status <> ? AND billing_status <> ?", saleID, "NOTA_CREDITO", "cancelled", "rejected").
		Select("COALESCE(SUM(total - service_charge_amount), 0)").Scan(&credited).Error
	return money.RoundSunat(credited), err
}

// distributeNoteAmount reparte un importe (con IGV) entre las líneas elegidas, proporcional al valor
// de cada una. selectedIDs vacío = todas las líneas con valor. Los céntimos sobrantes van a la línea de
// mayor valor, así la suma es exacta al céntimo.
func distributeNoteAmount(origItems []database.TenantSaleItem, selectedIDs []uint, amount float64) (map[uint]float64, error) {
	amount = money.RoundSunat(amount)
	if amount <= 0 {
		return nil, errors.New("el monto a acreditar debe ser mayor a cero")
	}
	allowed := map[uint]bool{}
	for _, id := range selectedIDs {
		allowed[id] = true
	}
	var pool []database.TenantSaleItem
	var poolTotal float64
	for _, it := range origItems {
		if it.Total <= 0 {
			continue // líneas gratuitas / sin valor: no hay de dónde descontar
		}
		if len(allowed) > 0 && !allowed[it.ID] {
			continue
		}
		pool = append(pool, it)
		poolTotal += it.Total
	}
	if len(pool) == 0 {
		return nil, errors.New("las líneas elegidas no tienen valor sobre el cual descontar")
	}
	poolTotal = money.RoundSunat(poolTotal)
	if amount > poolTotal+0.005 {
		return nil, fmt.Errorf("el monto S/ %.2f supera el valor de las líneas elegidas (S/ %.2f)", amount, poolTotal)
	}
	out := make(map[uint]float64, len(pool))
	var assigned float64
	for _, it := range pool {
		a := money.RoundSunat(amount * it.Total / poolTotal)
		out[it.ID] = a
		assigned = money.RoundSunat(assigned + a)
	}
	// Ajuste de céntimos sobre la línea de mayor valor (sin pasarse de su propio total).
	diff := money.RoundSunat(amount - assigned)
	if diff != 0 {
		sort.SliceStable(pool, func(i, j int) bool { return pool[i].Total > pool[j].Total })
		for _, it := range pool {
			adj := money.RoundSunat(out[it.ID] + diff)
			if adj >= 0 && adj <= it.Total+0.005 {
				out[it.ID] = adj
				diff = 0
				break
			}
		}
		if diff != 0 {
			return nil, errors.New("no se pudo repartir el monto entre las líneas")
		}
	}
	return out, nil
}

// splitLineAmount separa un importe con IGV en base + impuesto según la afectación de la línea.
// La tasa sale de la propia línea (la de la venta), no de la configuración actual de la empresa.
func splitLineAmount(orig database.TenantSaleItem, amount float64) (subtotal, taxAmount float64) {
	aff := strings.TrimSpace(orig.IgvAffectationType)
	if aff == "" {
		aff = "10"
	}
	if !tax.IsGravado(aff) || tax.IsBonificacionGravada(aff) {
		return money.RoundSunat(amount), 0
	}
	rate := orig.TaxRate
	if rate <= 0 && orig.Subtotal > 0 && orig.TaxAmount > 0 {
		rate = math.Round(orig.TaxAmount/orig.Subtotal*10000) / 100
	}
	if rate <= 0 {
		return money.RoundSunat(amount), 0
	}
	subtotal = money.RoundSunat(amount / (1 + rate/100))
	taxAmount = money.RoundSunat(amount - subtotal)
	return subtotal, taxAmount
}

// buildAmountNoteItems arma las líneas de una nota por monto. amounts: importe (con IGV) por línea
// original. credited: lo ya acreditado antes en la venta (creditedAmountOnSale).
func buildAmountNoteItems(originalSaleID uint, origItems []database.TenantSaleItem, amounts map[uint]float64, credited float64) ([]database.TenantSaleItem, float64, float64, float64, error) {
	if len(amounts) == 0 {
		return nil, 0, 0, 0, errors.New("indique el monto a acreditar")
	}
	byID := make(map[uint]database.TenantSaleItem, len(origItems))
	var saleValue float64
	for _, it := range origItems {
		byID[it.ID] = it
		saleValue += it.Total
	}
	saleValue = money.RoundSunat(saleValue)

	ids := make([]uint, 0, len(amounts))
	for id := range amounts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	items := make([]database.TenantSaleItem, 0, len(ids))
	var subtotal, taxAmount, total float64
	for _, id := range ids {
		orig, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, fmt.Errorf("el ítem %d no pertenece a la venta %d", id, originalSaleID)
		}
		amt := money.RoundSunat(amounts[id])
		if amt <= 0 {
			return nil, 0, 0, 0, fmt.Errorf("monto inválido para %q: debe ser mayor a cero", orig.Description)
		}
		if orig.Total <= 0 {
			return nil, 0, 0, 0, fmt.Errorf("%q no tiene valor sobre el cual descontar", orig.Description)
		}
		if amt > orig.Total+0.005 {
			return nil, 0, 0, 0, fmt.Errorf("monto inválido para %q: S/ %.2f supera el valor de la línea (S/ %.2f)", orig.Description, amt, orig.Total)
		}
		sub, tx := splitLineAmount(orig, amt)
		unitPrice := orig.UnitPrice
		if orig.Total > 0 {
			unitPrice = orig.UnitPrice * amt / orig.Total
		}
		items = append(items, database.TenantSaleItem{
			ProductID:          orig.ProductID,
			PresentationID:     orig.PresentationID,
			SaleUnitID:         orig.SaleUnitID,
			Code:               orig.Code,
			Description:        orig.Description,
			Unit:               orig.Unit,
			Quantity:           orig.Quantity,
			UnitPrice:          unitPrice,
			TaxRate:            orig.TaxRate,
			IgvAffectationType: orig.IgvAffectationType,
			Subtotal:           sub,
			TaxAmount:          tx,
			Total:              amt,
			PackLabel:          orig.PackLabel,
			// OriginalSaleItemID nil a propósito: ajuste de valor, no devolución (ver comentario del archivo).
		})
		subtotal = money.RoundSunat(subtotal + sub)
		taxAmount = money.RoundSunat(taxAmount + tx)
		total = money.RoundSunat(total + amt)
	}
	if remaining := money.RoundSunat(saleValue - credited); total > remaining+0.005 {
		if remaining < 0 {
			remaining = 0
		}
		return nil, 0, 0, 0, fmt.Errorf(
			"el monto S/ %.2f supera lo que aún se puede acreditar de esta venta: valor S/ %.2f, ya acreditado S/ %.2f, disponible S/ %.2f",
			total, saleValue, credited, remaining,
		)
	}
	return items, subtotal, taxAmount, total, nil
}

// resolveAmountNoteAmounts convierte la solicitud (total a repartir o importes por línea) en
// importe por línea original.
func resolveAmountNoteAmounts(origItems []database.TenantSaleItem, selections []NoteItemSelection, totalAmount float64) (map[uint]float64, error) {
	var explicit bool
	for _, s := range selections {
		if s.Amount > 0 {
			explicit = true
		}
	}
	if explicit && totalAmount > 0 {
		return nil, errors.New("indique el monto total o el monto por línea, no ambos")
	}
	if explicit {
		out := make(map[uint]float64, len(selections))
		for _, s := range selections {
			if s.Amount <= 0 {
				continue
			}
			if _, dup := out[s.OriginalItemID]; dup {
				return nil, fmt.Errorf("el ítem %d está repetido en la selección", s.OriginalItemID)
			}
			out[s.OriginalItemID] = s.Amount
		}
		return out, nil
	}
	ids := make([]uint, 0, len(selections))
	for _, s := range selections {
		ids = append(ids, s.OriginalItemID)
	}
	return distributeNoteAmount(origItems, ids, totalAmount)
}
