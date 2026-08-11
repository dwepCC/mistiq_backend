package service

import (
	"tukifac/pkg/database"
)

// LinkedReversionSummary reversión RR vinculada a un CRE/CPE (match payload_json).
type LinkedReversionSummary struct {
	ID          uint   `json:"id"`
	Correlativo string `json:"correlativo"`
	Status      string `json:"status"`
	Ticket      string `json:"ticket,omitempty"`
	SunatCode   string `json:"sunat_code,omitempty"`
	Motivo      string `json:"motivo,omitempty"`
}

// LinkedFiscalDocSummary resumen CRE/CPE vinculado a compra o venta origen.
type LinkedFiscalDocSummary struct {
	ID              uint                    `json:"id"`
	SaleID          *uint                   `json:"sale_id,omitempty"`
	Series          string                  `json:"series"`
	Correlative     string                  `json:"correlative"`
	Status          string                  `json:"status"`
	BillingStatus   string                  `json:"billing_status,omitempty"`
	DocKind         string                  `json:"doc_kind"`
	SunatCode       string                  `json:"sunat_code,omitempty"`
	SunatMessage    string                  `json:"sunat_message,omitempty"`
	LinkedReversion *LinkedReversionSummary `json:"linked_reversion,omitempty"`
}

func linkedSummaryFromRetention(rec *database.TenantRetention, billingStatus string) LinkedFiscalDocSummary {
	if rec == nil {
		return LinkedFiscalDocSummary{}
	}
	return LinkedFiscalDocSummary{
		ID:            rec.ID,
		SaleID:        rec.SaleID,
		Series:        rec.Series,
		Correlative:   rec.Correlative,
		Status:        rec.Status,
		BillingStatus: billingStatus,
		DocKind:       "retention",
		SunatCode:     rec.SunatCode,
		SunatMessage:  rec.SunatMessage,
	}
}

func linkedSummaryFromPerception(rec *database.TenantPerception, billingStatus string) LinkedFiscalDocSummary {
	if rec == nil {
		return LinkedFiscalDocSummary{}
	}
	return LinkedFiscalDocSummary{
		ID:            rec.ID,
		SaleID:        rec.SaleID,
		Series:        rec.Series,
		Correlative:   rec.Correlative,
		Status:        rec.Status,
		BillingStatus: billingStatus,
		DocKind:       "perception",
		SunatCode:     rec.SunatCode,
		SunatMessage:  rec.SunatMessage,
	}
}

func (s *BillingService) billingStatusForSaleID(saleID uint) string {
	if saleID == 0 {
		return ""
	}
	var sale database.TenantSale
	if s.db.Select("billing_status").First(&sale, saleID).Error != nil {
		return ""
	}
	return sale.BillingStatus
}

func (s *BillingService) GetLinkedRetentionByPurchaseID(purchaseID uint) (*LinkedFiscalDocSummary, error) {
	if purchaseID == 0 {
		return nil, nil
	}
	var rec database.TenantRetention
	if err := s.db.Where("purchase_id = ?", purchaseID).Order("created_at DESC").First(&rec).Error; err != nil {
		return nil, nil
	}
	bs := ""
	if rec.SaleID != nil {
		bs = s.billingStatusForSaleID(*rec.SaleID)
	}
	out := linkedSummaryFromRetention(&rec, bs)
	if rev := s.findLinkedReversion("20", rec.Series, rec.Correlative); rev != nil {
		out.LinkedReversion = rev
	}
	return &out, nil
}

func (s *BillingService) GetLinkedPerceptionBySourceSaleID(sourceSaleID uint) (*LinkedFiscalDocSummary, error) {
	if sourceSaleID == 0 {
		return nil, nil
	}
	var rec database.TenantPerception
	if err := s.db.Where("source_sale_id = ?", sourceSaleID).Order("created_at DESC").First(&rec).Error; err != nil {
		return nil, nil
	}
	bs := ""
	if rec.SaleID != nil {
		bs = s.billingStatusForSaleID(*rec.SaleID)
	}
	out := linkedSummaryFromPerception(&rec, bs)
	if rev := s.findLinkedReversion("40", rec.Series, rec.Correlative); rev != nil {
		out.LinkedReversion = rev
	}
	return &out, nil
}
