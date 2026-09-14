package service

import (
	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	"tukifac/pkg/paymentcondition"

	"gorm.io/gorm"
)

// applyCreditTermsToInvoicePayload arma cac:PaymentTerms (FormaPago=Credito) + una fila por
// cuota. SUNAT exige (código 3251) que cuando FormaPago es Credito, el nodo lleve el "Monto neto
// pendiente de pago" — sin eso rechaza el comprobante. facturador.InvoiceFormaPago.Monto lleva
// `json:"monto,omitempty"`, así que dejarlo en su zero-value (0.0) hace que el campo directamente
// desaparezca del JSON en vez de viajar como 0 — el nodo <cbc:Amount> nunca se genera.
func applyCreditTermsToInvoicePayload(db *gorm.DB, sale *database.TenantSale, payload *facturador.InvoicePayload) {
	if payload == nil || sale == nil {
		return
	}
	if !paymentcondition.IsCreditCode(sale.PaymentConditionCode) && sale.Status != "credit" {
		return
	}
	var rows []database.TenantSaleCreditInstallment
	if db.Where("sale_id = ?", sale.ID).Order("installment_no ASC").Find(&rows).Error != nil || len(rows) == 0 {
		// Sin filas de cuotas (venta antigua/migrada sin plan de pagos registrado, o falla de
		// consulta): no hay desglose por cuota, pero igual hay que declarar ALGÚN monto
		// pendiente — sale.Total es la aproximación conservadora más segura (nunca 0, nunca
		// vacío) en vez de dejar el nodo ausente y que SUNAT rechace el comprobante entero.
		payload.FormaPago = &facturador.InvoiceFormaPago{Tipo: "Credito", Moneda: payload.TipoMoneda, Monto: sale.Total}
		return
	}
	cuotas := make([]facturador.InvoiceCuota, len(rows))
	pendingTotal := 0.0
	for i, row := range rows {
		cuotas[i] = facturador.InvoiceCuota{
			Moneda:    row.Currency,
			Monto:     row.Amount,
			FechaPago: facturador.FormatFiscalDateTime(row.DueDate),
		}
		pendingTotal += row.Amount
	}
	// Monto Neto Pendiente de Pago = suma de las cuotas (validateCreditInstallments ya
	// garantiza al crear la venta que esa suma cubre el saldo a crédito real).
	payload.FormaPago = &facturador.InvoiceFormaPago{Tipo: "Credito", Moneda: payload.TipoMoneda, Monto: pendingTotal}
	payload.Cuotas = cuotas
}
