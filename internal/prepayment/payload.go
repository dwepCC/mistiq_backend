package prepayment

import (
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	sunatpre "tukifac/pkg/sunat/prepayment"
)

const pdfExtraPrepaymentLabel = "COMPROBANTE DE ANTICIPO"

// ApplyEmitToInvoicePayload aplica tipoOperacion configurado y parámetros PDF para emisión de anticipo.
// Debe invocarse después de salecontext/detracción para no ser sobrescrito.
func ApplyEmitToInvoicePayload(payload *facturador.InvoicePayload, voucher *database.TenantSalePrepaymentVoucher) {
	if payload == nil || voucher == nil {
		return
	}
	payload.TipoOperacion = sunatpre.EmitOperationTypeCode()
	mergePrepaymentPDFParameters(payload)
}

func mergePrepaymentPDFParameters(payload *facturador.InvoicePayload) {
	if payload == nil {
		return
	}
	extras := []facturador.InvoicePDFExtra{
		{
			Name:  "Tipo operación",
			Value: sunatpre.EmitOperationFullLabel(),
		},
		{
			Name:  sunatpre.PDFExtraPrepaymentEmit,
			Value: sunatpre.PDFExtraPrepaymentEmitValue,
		},
	}
	if payload.Parameters == nil {
		payload.Parameters = &facturador.InvoicePDFParameters{
			User: facturador.InvoicePDFUserParameters{Extras: extras},
		}
		return
	}
	for _, extra := range extras {
		if !hasPDFExtra(payload, extra.Name) {
			payload.Parameters.User.Extras = append(payload.Parameters.User.Extras, extra)
		}
	}
}

func hasPDFExtra(payload *facturador.InvoicePayload, name string) bool {
	for _, e := range payload.Parameters.User.Extras {
		if strings.EqualFold(strings.TrimSpace(e.Name), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// IsEmitVoucher indica si el voucher corresponde a una emisión de anticipo.
func IsEmitVoucher(v *database.TenantSalePrepaymentVoucher) bool {
	return v != nil && v.SaleID > 0
}

// EmitPDFLabel retorna la etiqueta para impresión ticket/jsPDF.
func EmitPDFLabel() string {
	return pdfExtraPrepaymentLabel
}

// IsPrepaymentEmitSale indica si la venta es emisión de anticipo según voucher o tipo operación.
func IsPrepaymentEmitSale(voucher *database.TenantSalePrepaymentVoucher, operationTypeCode string) bool {
	if IsEmitVoucher(voucher) {
		return true
	}
	return sunatpre.IsAllowedEmitOperationType(operationTypeCode) &&
		operationTypeCode == sunatpre.EmitOperationTypeCode()
}
