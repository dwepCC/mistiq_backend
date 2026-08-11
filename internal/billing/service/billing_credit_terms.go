package service

import (
	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	"tukifac/pkg/paymentcondition"

	"gorm.io/gorm"
)

func applyCreditTermsToInvoicePayload(db *gorm.DB, sale *database.TenantSale, payload *facturador.InvoicePayload) {
	if payload == nil || sale == nil {
		return
	}
	if !paymentcondition.IsCreditCode(sale.PaymentConditionCode) && sale.Status != "credit" {
		return
	}
	var rows []database.TenantSaleCreditInstallment
	if db.Where("sale_id = ?", sale.ID).Order("installment_no ASC").Find(&rows).Error != nil || len(rows) == 0 {
		payload.FormaPago = &facturador.InvoiceFormaPago{Tipo: "Credito", Moneda: payload.TipoMoneda}
		return
	}
	cuotas := make([]facturador.InvoiceCuota, len(rows))
	for i, row := range rows {
		cuotas[i] = facturador.InvoiceCuota{
			Moneda:    row.Currency,
			Monto:     row.Amount,
			FechaPago: facturador.FormatFiscalDateTime(row.DueDate),
		}
	}
	payload.FormaPago = &facturador.InvoiceFormaPago{Tipo: "Credito", Moneda: payload.TipoMoneda}
	payload.Cuotas = cuotas
}
