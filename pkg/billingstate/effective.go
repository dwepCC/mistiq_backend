package billingstate

import (
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// Valores canónicos de tenant_sales.billing_status (UI tenant / restaurante).
const (
	BillingPending  = "pending"
	BillingSent     = "sent"
	BillingAccepted = "accepted"
	BillingRejected = "rejected"
	BillingError    = "error"
)

var billingStatusRank = map[string]int{
	BillingPending:  0,
	BillingSent:     1,
	BillingError:    2,
	BillingRejected: 3,
	BillingAccepted: 4,
}

// NormalizeBillingStatus normaliza a enum UI (pending|sent|accepted|rejected|error).
func NormalizeBillingStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case BillingAccepted:
		return BillingAccepted
	case BillingRejected:
		return BillingRejected
	case BillingError:
		return BillingError
	case BillingSent:
		return BillingSent
	case BillingPending:
		return BillingPending
	default:
		return BillingPending
	}
}

func billingStatusFromInvoice(inv *database.TenantInvoice) string {
	if inv == nil {
		return BillingPending
	}
	p := NormalizePipeline(inv.PipelineStatus)
	if p == DRAFT && inv.ID > 0 {
		p = inferPipelineFromLegacy(inv)
	}
	return LegacyBillingStatus(p)
}

// EffectiveBillingStatus combina venta + invoice; nunca degrada un estado más avanzado.
func EffectiveBillingStatus(sale *database.TenantSale, inv *database.TenantInvoice) string {
	fromSale := BillingPending
	if sale != nil {
		fromSale = NormalizeBillingStatus(sale.BillingStatus)
	}
	fromInv := billingStatusFromInvoice(inv)
	if billingStatusRank[fromInv] >= billingStatusRank[fromSale] {
		return fromInv
	}
	return fromSale
}

// ReconcileSaleBillingStatus alinea tenant_sales con invoice si hay desvío.
func ReconcileSaleBillingStatus(db *gorm.DB, saleID uint) (string, error) {
	if db == nil || saleID == 0 {
		return BillingPending, nil
	}
	var sale database.TenantSale
	if err := db.First(&sale, saleID).Error; err != nil {
		return "", err
	}
	var inv database.TenantInvoice
	invErr := db.Where("sale_id = ?", saleID).First(&inv).Error
	var invPtr *database.TenantInvoice
	if invErr == nil {
		invPtr = &inv
	}
	effective := EffectiveBillingStatus(&sale, invPtr)
	if effective != sale.BillingStatus {
		_ = db.Model(&database.TenantSale{}).Where("id = ?", saleID).
			Update("billing_status", effective).Error
	}
	return effective, nil
}

// EnrichSalesBillingStatus corrige billing_status en memoria y persiste desvíos (listados UI).
func EnrichSalesBillingStatus(db *gorm.DB, sales []database.TenantSale) {
	if db == nil || len(sales) == 0 {
		return
	}
	ids := make([]uint, len(sales))
	for i := range sales {
		ids[i] = sales[i].ID
	}
	var invoices []database.TenantInvoice
	_ = db.Where("sale_id IN ?", ids).Find(&invoices).Error
	bySale := make(map[uint]*database.TenantInvoice, len(invoices))
	for i := range invoices {
		bySale[invoices[i].SaleID] = &invoices[i]
	}
	// Los desvíos se persisten agrupados por estado destino (un UPDATE por estado, no uno por venta):
	// esto corre dentro de un GET de listado y con miles de filas generaba miles de escrituras.
	idsByStatus := make(map[string][]uint)
	for i := range sales {
		inv := bySale[sales[i].ID]
		effective := EffectiveBillingStatus(&sales[i], inv)
		if effective == sales[i].BillingStatus {
			continue
		}
		sales[i].BillingStatus = effective
		idsByStatus[effective] = append(idsByStatus[effective], sales[i].ID)
	}
	for status, group := range idsByStatus {
		for start := 0; start < len(group); start += 500 {
			end := start + 500
			if end > len(group) {
				end = len(group)
			}
			_ = db.Model(&database.TenantSale{}).Where("id IN ?", group[start:end]).
				Update("billing_status", status).Error
		}
	}
}
