package service

import (
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	"tukifac/pkg/tax"
)

// BuildInvoiceDetailsFromSaleItems construye líneas Lycet/Greenter con AllowanceCharge explícito.
//
// Greenter (thegreenter/demo):
//   - Descuento línea cod 00: mtoValorUnitario bruto; base/IGV/precio POST-línea (sin global).
//   - Descuento global cod 02: líneas PRE-global; totales documento POST-global (desde BD).
func BuildInvoiceDetailsFromSaleItems(items []database.TenantSaleItem, companyTaxRate float64, normUnit func(string) string) ([]facturador.InvoiceDetail, error) {
	details := make([]facturador.InvoiceDetail, len(items))
	for i, item := range items {
		aff := strings.TrimSpace(item.IgvAffectationType)
		if aff == "" {
			return nil, fmt.Errorf("el ítem «%s» no tiene tipo de afectación IGV", strings.TrimSpace(item.Description))
		}
		cantidad := item.Quantity
		if cantidad <= 0 {
			return nil, fmt.Errorf("el ítem «%s» tiene cantidad inválida", strings.TrimSpace(item.Description))
		}
		codProd := strings.TrimSpace(item.Code)
		if codProd == "" {
			return nil, fmt.Errorf("el ítem «%s» no tiene código de producto", strings.TrimSpace(item.Description))
		}
		desc := strings.TrimSpace(item.Description)
		if desc == "" {
			return nil, fmt.Errorf("ítem en posición %d sin descripción", i+1)
		}

		preGlobal := linePreGlobalBase(item)
		hasGlobal := item.GlobalDiscountSubtotal > 0
		mtoValorVenta := preGlobal

		var mtoValorUnitario float64
		if item.LineDiscountSubtotal > 0 {
			mtoValorUnitario = round2((preGlobal + item.LineDiscountSubtotal) / cantidad)
		} else {
			mtoValorUnitario = round2(preGlobal / cantidad)
		}

		rate := lineIgvRateForPayload(aff, item.TaxRate, companyTaxRate)
		porcentajeIgv := linePorcentajeIgvForPayload(aff, item.TaxRate, companyTaxRate)

		var mtoBaseIgv, igv, mtoPrecioUnitario float64
		if hasGlobal {
			mtoBaseIgv = preGlobal
			if rate > 0 {
				igv = round2(preGlobal * (rate / 100))
				mtoPrecioUnitario = round2((preGlobal + igv) / cantidad)
			} else {
				igv = 0
				mtoPrecioUnitario = round2(preGlobal / cantidad)
			}
		} else {
			mtoBaseIgv = round2(item.Subtotal)
			igv = round2(item.TaxAmount)
			mtoPrecioUnitario = round2((mtoBaseIgv + igv) / cantidad)
		}

		var lineDescuentos []facturador.InvoiceCharge
		if item.LineDiscountSubtotal > 0 {
			grossBase := round2(mtoValorVenta + item.LineDiscountSubtotal)
			lineDescuentos = []facturador.InvoiceCharge{
				chargeFromAmounts(tax.AllowanceCodeLineDiscountAffectsIGV, grossBase, item.LineDiscountSubtotal),
			}
		}

		var mtoValorGratuito float64
		if tax.IsBonificacionGravada(aff) {
			// SUNAT UBL 2.1 operación gratuita (Guía factura/boleta 2.1):
			// - cac:Price/cbc:PriceAmount → 0 (error 2640 si lleva base onerosa).
			// - cac:PricingReference PriceTypeCode 02 → mtoValorGratuito (ref. unitario con IGV).
			// - LineExtensionAmount (mtoValorVenta) y TaxTotal conservan base/IGV referencial.
			// - tipAfeIgv 15; Greenter stock mapea 15 → tributo 9996 (default).
			mtoValorGratuito = round2(mtoValorVenta / cantidad)
			mtoPrecioUnitario = 0
			mtoValorUnitario = 0
		}

		// Impuesto cobrable de línea (Greenter TotalImpuestos). En gravado no oneroso 11–16 el
		// legacy deja total_taxes≈0; el IGV referencial va solo en detail.Igv (TaxSubtotal).
		linePlasticBag := 0.0 // ICBPER por línea: reservado; legacy suma bolsa en total_taxes 11–16.
		totalImpuestos := tax.LineChargeableTotalImpuestos(aff, igv, linePlasticBag)

		details[i] = facturador.InvoiceDetail{
			Unidad:            normUnit(item.Unit),
			Cantidad:          cantidad,
			CodProducto:       codProd,
			Descripcion:       desc,
			MtoValorUnitario:  mtoValorUnitario,
			MtoValorVenta:     mtoValorVenta,
			TipAfeIgv:         aff,
			MtoBaseIgv:        mtoBaseIgv,
			PorcentajeIgv:     porcentajeIgv,
			Igv:               igv,
			TotalImpuestos:    totalImpuestos,
			MtoPrecioUnitario: mtoPrecioUnitario,
			MtoValorGratuito:  mtoValorGratuito,
			Descuentos:        lineDescuentos,
		}
	}
	return details, nil
}

// linePreGlobalBase devuelve la base de línea después del descuento por línea y antes del global (UBL LineExtensionAmount).
func linePreGlobalBase(item database.TenantSaleItem) float64 {
	if item.LineDiscountSubtotal <= 0 && item.GlobalDiscountSubtotal <= 0 {
		return round2(item.Subtotal)
	}
	return round2(item.Subtotal + item.GlobalDiscountSubtotal)
}

// lineIgvRateForPayload tasa IGV para calcular igv PRE-global en línea (0 en exonerado/inafecto).
func lineIgvRateForPayload(aff string, itemRate, companyTaxRate float64) float64 {
	switch aff {
	case "20", "30":
		return 0
	default:
		if !tax.IsGravado(aff) {
			return 0
		}
		if itemRate > 0 {
			return itemRate
		}
		return companyTaxRate
	}
}

// linePorcentajeIgvForPayload porcentaje para <cbc:Percent> (Catálogo 07 gravado → tasa empresa).
func linePorcentajeIgvForPayload(aff string, itemRate, companyTaxRate float64) float64 {
	if !tax.IsGravado(aff) {
		return 0
	}
	if itemRate > 0 {
		return round2(itemRate)
	}
	return round2(companyTaxRate)
}

// BuildGlobalInvoiceDiscounts genera descuentos globales UBL (cod 02) para el documento.
// sumOtrosDescuentos retornado es 0: Greenter no lo establece para cod 02 (afecta base).
func BuildGlobalInvoiceDiscounts(sale *database.TenantSale, items []database.TenantSaleItem) ([]facturador.InvoiceCharge, float64) {
	amount := round2(sale.GlobalDiscountAmount)
	if amount <= 0 {
		return nil, 0
	}
	var baseBeforeGlobal float64
	for _, it := range items {
		baseBeforeGlobal += linePreGlobalBase(it)
	}
	baseBeforeGlobal = round2(baseBeforeGlobal)
	if baseBeforeGlobal <= 0 {
		return nil, 0
	}
	return []facturador.InvoiceCharge{
		chargeFromAmounts(tax.AllowanceCodeGlobalDiscountAffectsIGV, baseBeforeGlobal, amount),
	}, 0
}

func chargeFromAmounts(codTipo string, montoBase, monto float64) facturador.InvoiceCharge {
	montoBase = round2(montoBase)
	monto = round2(monto)
	factor := 0.0
	if montoBase > 0 {
		factor = monto / montoBase
	}
	return facturador.InvoiceCharge{
		CodTipo:   codTipo,
		Factor:    factor,
		Monto:     monto,
		MontoBase: montoBase,
	}
}
