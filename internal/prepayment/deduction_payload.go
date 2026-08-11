package prepayment

import (
	"strconv"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	sunatpre "tukifac/pkg/sunat/prepayment"
)

// SaleDeductionNet totales netos persistidos en tenant_sales tras deducir anticipos (PHP: total, total_igv, total_taxed).
type SaleDeductionNet struct {
	Subtotal  float64
	TaxAmount float64
	Total     float64
}

// ApplyDeductionToInvoicePayload agrega anticipos deducidos y descuento global 04/05/06 al payload SUNAT.
//
// Legacy PHP (invoice_generate.vue + invoice.blade.php):
//   - total_value / valorVenta (LineExtensionAmount) = suma bruta bases ítems, NO se reduce con anticipo
//   - total_taxed / mtoOperGravadas = base gravada NETA (después descuento 04)
//   - total_igv / mtoIGV = IGV recalculado sobre base neta (total_taxes)
//   - subtotal (TaxInclusiveAmount) = bruto con IGV (antes de restar anticipo en lo pagadero)
//   - total_prepayment / totalAnticipos (PrepaidAmount) = suma totales deducidos
//   - total / mtoImpVenta (PayableAmount) = importe neto a pagar
//
// Los importes netos provienen de la venta guardada; no se recalculan en billing para evitar negativos.
func ApplyDeductionToInvoicePayload(
	payload *facturador.InvoicePayload,
	applications []database.TenantSalePrepaymentApplication,
	applyResult sunatpre.ApplyDeductionResult,
	grossTotals sunatpre.SaleGroupTotals,
	saleNet SaleDeductionNet,
) {
	if payload == nil || len(applications) == 0 {
		return
	}
	group := strings.TrimSpace(applications[0].AffectationGroup)
	if group == "" {
		group = groupFromDiscountCode(applyResult.DiscountCode)
	}

	anticipos := make([]facturador.InvoicePrepayment, 0, len(applications))
	var totalAnticipos float64
	var baseSum float64
	for _, app := range applications {
		anticipos = append(anticipos, facturador.InvoicePrepayment{
			TipoDocRel: app.RelatedDocType,
			NroDocRel:  app.DocumentNumber,
			Total:      app.Total,
		})
		totalAnticipos += app.Total
		baseSum += app.Amount
	}
	payload.Anticipos = anticipos
	payload.TotalAnticipos = round2(totalAnticipos)

	netTotal := clampMoney(saleNet.Total)
	netTax := clampMoney(saleNet.TaxAmount)
	netSubtotal := clampMoney(saleNet.Subtotal)

	gravadoNet, exoneradoNet, inafectoNet := netOperTotalsByGroup(grossTotals, group, baseSum, netSubtotal)

	payload.ValorVenta = clampMoney(grossTotals.Subtotal)
	payload.MtoOperGravadas = gravadoNet
	payload.MtoOperExoneradas = exoneradoNet
	payload.MtoOperInafectas = inafectoNet
	payload.MtoIGV = netTax
	payload.TotalImpuestos = netTax

	grossSubTotal := round2(netTotal + totalAnticipos)
	if grossSubTotal <= 0 && applyResult.DeductionTotal > 0 {
		grossSubTotal = round2(netTotal + applyResult.DeductionTotal)
	}
	payload.SubTotal = grossSubTotal
	payload.MtoImpVenta = netTotal

	if applyResult.DiscountAmount > 0 {
		charge := facturador.InvoiceCharge{
			CodTipo:   applyResult.DiscountCode,
			Factor:    applyResult.DiscountFactor,
			Monto:     applyResult.DiscountAmount,
			MontoBase: applyResult.DiscountBase,
		}
		payload.Descuentos = append(payload.Descuentos, charge)
	}
}

func netOperTotalsByGroup(
	gross sunatpre.SaleGroupTotals,
	group string,
	deductionBase float64,
	saleNetSubtotal float64,
) (gravado, exonerado, inafecto float64) {
	exonerado = clampMoney(gross.ExoneradoSubtotal)
	inafecto = clampMoney(gross.InafectoSubtotal)
	gravado = clampMoney(gross.GravadoSubtotal)

	switch group {
	case sunatpre.AffectationGravado:
		gravado = clampMoney(gross.GravadoSubtotal - deductionBase)
		if gravado <= 0 && saleNetSubtotal > 0 {
			gravado = clampMoney(saleNetSubtotal - exonerado - inafecto)
		}
	case sunatpre.AffectationExonerado:
		exonerado = clampMoney(gross.ExoneradoSubtotal - deductionBase)
		if exonerado <= 0 && saleNetSubtotal > 0 {
			exonerado = clampMoney(saleNetSubtotal - gravado - inafecto)
		}
	case sunatpre.AffectationInafecto:
		inafecto = clampMoney(gross.InafectoSubtotal - deductionBase)
		if inafecto <= 0 && saleNetSubtotal > 0 {
			inafecto = clampMoney(saleNetSubtotal - gravado - exonerado)
		}
	}
	return gravado, exonerado, inafecto
}

func clampMoney(v float64) float64 {
	v = round2(v)
	if v < 0 {
		return 0
	}
	return v
}

func groupFromDiscountCode(code string) string {
	switch code {
	case sunatpre.DiscountCodeGravadoAnticipo:
		return sunatpre.AffectationGravado
	case sunatpre.DiscountCodeExoneradoAnticipo:
		return sunatpre.AffectationExonerado
	case sunatpre.DiscountCodeInafectoAnticipo:
		return sunatpre.AffectationInafecto
	default:
		return sunatpre.AffectationGravado
	}
}

func round2(v float64) float64 {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
