package service

import (
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/tax"
)

// Notas de crédito/débito sobre comprobantes con descuento global o anticipo.
//
// La factura declara las líneas BRUTAS y el descuento global / la deducción del anticipo como un nodo aparte (AllowanceCharge
// 02/04); su total ya viene neto (p. ej. línea 177 − anticipo 30 = 147). La plantilla de la nota (Greenter notacr2.1) NO tiene ese
// nodo: sus líneas deben sumar por sí solas el total de la nota. Copiar las líneas brutas dejaba la nota con base 150 + IGV 27 y un
// importe de 147, y SUNAT la rechazaba con 3280 («el importe total del comprobante no coincide con el valor calculado»).
//
// netNoteItemsForPayload devuelve las líneas con el descuento global ya aplicado: quita GlobalDiscountSubtotal (que haría al
// armador volver a sumar la base previa al descuento) y, si aun así las líneas no suman la base y el IGV de la nota, las reparte
// proporcionalmente (el último con peso absorbe el redondeo) hasta que coincidan exactamente. No toca la venta guardada.
func netNoteItemsForPayload(items []database.TenantSaleItem, note *database.TenantSale) []database.TenantSaleItem {
	out := make([]database.TenantSaleItem, len(items))
	copy(out, items)
	for i := range out {
		out[i].GlobalDiscountSubtotal = 0
	}
	if note == nil {
		return out
	}

	// Solo las líneas cobrables entran en la base/IGV de la cabecera (las bonificaciones gravadas van aparte).
	var sumSub, sumTax float64
	var lastSub, lastTax = -1, -1
	for i, it := range out {
		if tax.IsBonificacionGravada(it.IgvAffectationType) {
			continue
		}
		sumSub += it.Subtotal
		sumTax += it.TaxAmount
		if it.Subtotal > 0 {
			lastSub = i
		}
		if it.TaxAmount > 0 {
			lastTax = i
		}
	}
	sumSub, sumTax = money.RoundSunat(sumSub), money.RoundSunat(sumTax)
	wantSub, wantTax := money.RoundSunat(note.Subtotal), money.RoundSunat(note.TaxAmount)

	const eps = 0.005
	subDiff := wantSub - sumSub
	taxDiff := wantTax - sumTax
	if (subDiff < eps && subDiff > -eps && taxDiff < eps && taxDiff > -eps) || sumSub <= 0 {
		return out
	}

	var accSub, accTax float64
	for i := range out {
		if tax.IsBonificacionGravada(out[i].IgvAffectationType) {
			continue
		}
		if out[i].Subtotal > 0 {
			if i == lastSub {
				out[i].Subtotal = money.RoundSunat(wantSub - accSub)
			} else {
				out[i].Subtotal = money.RoundSunat(out[i].Subtotal * wantSub / sumSub)
			}
			accSub = money.RoundSunat(accSub + out[i].Subtotal)
		}
		if sumTax > 0 && out[i].TaxAmount > 0 {
			if i == lastTax {
				out[i].TaxAmount = money.RoundSunat(wantTax - accTax)
			} else {
				out[i].TaxAmount = money.RoundSunat(out[i].TaxAmount * wantTax / sumTax)
			}
			accTax = money.RoundSunat(accTax + out[i].TaxAmount)
		}
		out[i].Total = money.RoundSunat(out[i].Subtotal + out[i].TaxAmount)
	}
	return out
}
