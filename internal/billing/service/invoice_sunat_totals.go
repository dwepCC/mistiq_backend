package service

import (
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"
)

// InvoiceSunatTotals totales de cabecera UBL 2.1 (gravado cobrable vs operaciones gratuitas).
type InvoiceSunatTotals struct {
	MtoOperGravadas   float64
	MtoOperExoneradas float64
	MtoOperInafectas  float64
	MtoOperExportacion float64
	MtoIGV            float64
	MtoOperGratuitas  float64
	MtoIGVGratuitas   float64
	ValorVenta        float64
	TotalImpuestos    float64
	MtoImpVenta       float64 // importe cobrado al cliente (sale.Total)
}

// ComputeInvoiceSunatTotals agrupa ítems según SUNAT UBL 2.1.
//
// Bonificación gravada (código 15, sin cobro al cliente) → mtoOperGratuitas + mtoIGVGratuitas (tributo 9996).
// No se suma a mtoOperGravadas ni a mtoIGV cobrable.
func ComputeInvoiceSunatTotals(items []database.TenantSaleItem, saleTotal float64) InvoiceSunatTotals {
	var out InvoiceSunatTotals
	for _, item := range items {
		aff := strings.TrimSpace(item.IgvAffectationType)
		if aff == "" {
			aff = "10"
		}
		sub := round2(item.Subtotal)
		taxAmt := round2(item.TaxAmount)
		if tax.IsBonificacionGravada(aff) {
			out.MtoOperGratuitas = round2(out.MtoOperGratuitas + sub)
			out.MtoIGVGratuitas = round2(out.MtoIGVGratuitas + taxAmt)
			continue
		}
		switch aff {
		case "10":
			out.MtoOperGravadas = round2(out.MtoOperGravadas + sub)
			out.MtoIGV = round2(out.MtoIGV + taxAmt)
		case "20":
			out.MtoOperExoneradas = round2(out.MtoOperExoneradas + sub)
		case "30":
			out.MtoOperInafectas = round2(out.MtoOperInafectas + sub)
		case "40":
			// Exportación: sin IGV (tasa 0). Tributo 9995/EXP — mismo tratamiento que exonerado/
			// inafecto (TaxSubtotal por línea vía getTributoAfect en Greenter, ya soportado sin
			// cambios; ver lineIgvRateForPayload/linePorcentajeIgvForPayload).
			out.MtoOperExportacion = round2(out.MtoOperExportacion + sub)
		default:
			if tax.IsGravado(aff) {
				out.MtoOperGravadas = round2(out.MtoOperGravadas + sub)
				out.MtoIGV = round2(out.MtoIGV + taxAmt)
			}
		}
	}
	// SUNAT UBL 2.1: LegalMonetaryTotal/LineExtensionAmount (valorVenta) no incluye gratuitas.
	// Exportación (40) sí es parte del valor de venta —no está exenta de la operación, solo
	// del IGV— así que suma aquí igual que gravadas/exoneradas/inafectas.
	out.ValorVenta = round2(out.MtoOperGravadas + out.MtoOperExoneradas + out.MtoOperInafectas + out.MtoOperExportacion)
	// Guía SUNAT numeral 27 (Sumatoria IGV → TaxSubtotal tributo 1000): no incluye IGV de
	// transferencias gratuitas. El IGV referencial de bonificación 15 va en mtoIGVGratuitas
	// (tributo 9996) y no debe sumarse al TaxTotal/cbc:TaxAmount global (error 4301).
	out.TotalImpuestos = round2(out.MtoIGV)
	out.MtoImpVenta = round2(out.ValorVenta + out.MtoIGV)
	if saleTotal > 0 {
		out.MtoImpVenta = round2(saleTotal)
	}
	return out
}
