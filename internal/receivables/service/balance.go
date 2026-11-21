package service

import (
	"gorm.io/gorm"

	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/salescope"
	"tukifac/pkg/taxpayment"
)

const (
	BnStatusPending   = "pending"
	BnStatusConfirmed = "confirmed"
	BnStatusRejected  = "rejected"
)

// DirectPaid suma pagos directos (excluye SPOT y crédito interno).
func DirectPaid(payments []database.TenantSalePayment) float64 {
	var sum float64
	for _, p := range payments {
		if taxpayment.IsDetractionCode(p.Method) || paymentcondition.IsCreditCode(p.Method) {
			continue
		}
		sum += p.Amount
	}
	return sum
}

// SaleBalance calcula montos CxC directos y SPOT pendiente.
func SaleBalance(sale database.TenantSale, det *database.TenantSaleDetraccion, payments []database.TenantSalePayment) (
	directTarget, directPaid, directDue, spotAmount, spotPending float64,
	bnStatus string,
) {
	if det != nil && taxpayment.IsSelfDetractionStatus(det.BnConfirmationStatus) {
		// Autodetracción: el cliente paga el TOTAL de la factura al proveedor (que deposita la detracción en
		// su propia cuenta BN), así que no hay SPOT que el cliente deba acreditar ni confirmar en el BN.
		directTarget = det.InvoiceTotalPen
		if directTarget <= 0 {
			directTarget = sale.Total
		}
		bnStatus = taxpayment.BnStatusSelfPending
	} else if det != nil {
		directTarget = det.NetPayablePen
		spotAmount = det.DetractionAmountPen
		bnStatus = det.BnConfirmationStatus
		if bnStatus == "" {
			bnStatus = BnStatusPending
		}
		if spotAmount > 0 && bnStatus == BnStatusPending {
			spotPending = spotAmount
		}
	} else {
		directTarget = sale.Total
	}
	// Las notas de crédito aceptadas contra esta venta bajan lo que el cliente debe pagar (la factura no se edita: la nota la
	// corrige). Con detracción se resta igual: el depósito ya hecho no cambia, el resto a pagar baja lo que baja la factura.
	if sale.NoteCredited > 0 {
		directTarget = money.RoundDisplay(directTarget - sale.NoteCredited)
		if directTarget < 0 {
			directTarget = 0
		}
	}
	directPaid = DirectPaid(payments)
	directDue = money.RoundDisplay(directTarget - directPaid)
	if directDue < money.PaymentTolerance {
		directDue = 0
	}
	return
}

// HasOpenReceivable indica si la venta tiene saldo directo o SPOT BN pendiente.
//
// Una nota de crédito/débito (tenant_sales.doc_type IN NOTA_CREDITO/NOTA_DEBITO) NUNCA es una
// cuenta por cobrar: se crea con Status="paid" y Total>0 (ver CreateCreditNoteAndVoidSale), pero
// ese "Total" es el monto que la nota ANULA/AJUSTA de otra venta — no dinero que el cliente le
// deba al negocio. Sin esta exclusión, una NC recién creada (paid, sin TenantSalePayment) calcula
// directDue == su Total completo y queda como cobrable, permitiendo cobrarla por
// ReceivableService.Collect() como si fuera una venta real — el bug reportado: "Cobro
// FC01-00000001" apareciendo como ingreso en Cuentas Bancarias por el total de una nota de
// crédito. Mismo criterio que ya usa el arqueo de Caja (ver listOrphanSalesForSession,
// pkg/salescope/notes.go) — antes solo aplicado ahí, nunca a Cuentas por Cobrar.
func HasOpenReceivable(sale database.TenantSale, det *database.TenantSaleDetraccion, payments []database.TenantSalePayment) bool {
	if sale.Status == "cancelled" {
		return false
	}
	if salescope.IsNoteDocType(sale.DocType) {
		return false
	}
	_, _, directDue, _, spotPending, bnStatus := SaleBalance(sale, det, payments)
	if directDue >= money.PaymentTolerance {
		return true
	}
	if spotPending >= money.PaymentTolerance && bnStatus == BnStatusPending {
		return true
	}
	return false
}

// AttachNoteCredits llena sale.NoteCredited en cada venta con el total de sus notas de crédito aceptadas por SUNAT (no
// rechazadas ni anuladas). Es lo que hace que una nota que corrige el valor de una factura a crédito baje la cuenta por cobrar.
// Una venta anulada por completo (nota 01) ya sale de Cuentas por Cobrar por su estado; aquí cuentan las parciales y de valor.
func AttachNoteCredits(db *gorm.DB, sales []database.TenantSale) error {
	if len(sales) == 0 {
		return nil
	}
	ids := make([]uint, len(sales))
	for i := range sales {
		ids[i] = sales[i].ID
	}
	var rows []struct {
		OriginalSaleID uint
		Total          float64
	}
	if err := db.Model(&database.TenantSale{}).
		Select("original_sale_id, COALESCE(SUM(total), 0) AS total").
		Where("original_sale_id IN ? AND doc_type = ? AND billing_status = ? AND status <> ?", ids, "NOTA_CREDITO", "accepted", "cancelled").
		Group("original_sale_id").Scan(&rows).Error; err != nil {
		return err
	}
	byID := make(map[uint]float64, len(rows))
	for _, r := range rows {
		byID[r.OriginalSaleID] = r.Total
	}
	for i := range sales {
		sales[i].NoteCredited = money.RoundDisplay(byID[sales[i].ID])
	}
	return nil
}
