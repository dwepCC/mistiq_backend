package service

import (
	"log/slog"
	"strings"

	salesvc "tukifac/internal/sales/service"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
)

// PostFiscalAcceptSideEffects acciones tras aceptación SUNAT (NC anula venta, guía/retención/percepción sincronizan registro auxiliar).
func (s *BillingService) PostFiscalAcceptSideEffects(saleID uint, pipeline string) {
	p := billingstate.NormalizePipeline(pipeline)
	if p != billingstate.SUNAT_ACCEPTED && p != billingstate.OBSERVED {
		s.syncLinkedDespatchStatus(saleID, p)
		s.syncLinkedRetentionStatus(saleID, p)
		s.syncLinkedPerceptionStatus(saleID, p)
		return
	}

	var sale database.TenantSale
	if err := s.db.First(&sale, saleID).Error; err != nil {
		return
	}

	s.syncLinkedDespatchStatus(saleID, p)
	s.syncLinkedRetentionStatus(saleID, p)
	s.syncLinkedPerceptionStatus(saleID, p)

	if sale.DocType != "NOTA_CREDITO" || sale.OriginalSaleID == nil {
		return
	}
	origID := *sale.OriginalSaleID
	var orig database.TenantSale
	if err := s.db.First(&orig, origID).Error; err != nil {
		return
	}
	if orig.Status == "cancelled" {
		return
	}

	var inv database.TenantInvoice
	if err := s.db.Where("sale_id = ?", saleID).First(&inv).Error; err != nil {
		return
	}
	if !billingstate.HasAcceptanceEvidence(&inv) && p != billingstate.OBSERVED {
		if inv.SunatStatus != "accepted" && inv.SunatCDRCode != "0" {
			return
		}
	}

	saleSvc := salesvc.NewSaleService(s.db)
	if err := saleSvc.Cancel(origID, 0, "Anulado por nota de crédito aceptada por SUNAT"); err != nil {
		logger.L.Warn("nc_void_original_failed",
			slog.Uint64("tenant_id", uint64(s.centralTenantID)),
			slog.Uint64("nc_sale_id", uint64(saleID)),
			slog.Uint64("original_sale_id", uint64(origID)),
			slog.Any("error", err),
		)
		return
	}
	logger.L.Info("nc_void_original_ok",
		slog.Uint64("tenant_id", uint64(s.centralTenantID)),
		slog.Uint64("nc_sale_id", uint64(saleID)),
		slog.Uint64("original_sale_id", uint64(origID)),
	)
}

func (s *BillingService) syncLinkedDespatchStatus(saleID uint, pipeline string) {
	var despatch database.TenantDespatch
	if err := s.db.Where("sale_id = ?", saleID).First(&despatch).Error; err != nil {
		return
	}
	status := auxiliaryDocStatusFromPipeline(pipeline)
	if status == despatch.Status {
		return
	}
	_ = s.db.Model(&despatch).Updates(s.auxiliaryDocUpdatesFromInvoice(saleID, status)).Error
}

func (s *BillingService) syncLinkedRetentionStatus(saleID uint, pipeline string) {
	var rec database.TenantRetention
	if err := s.db.Where("sale_id = ?", saleID).First(&rec).Error; err != nil {
		return
	}
	status := auxiliaryDocStatusFromPipeline(pipeline)
	if status == rec.Status {
		return
	}
	_ = s.db.Model(&rec).Updates(s.auxiliaryDocUpdatesFromInvoice(saleID, status)).Error
}

func (s *BillingService) syncLinkedPerceptionStatus(saleID uint, pipeline string) {
	var rec database.TenantPerception
	if err := s.db.Where("sale_id = ?", saleID).First(&rec).Error; err != nil {
		return
	}
	status := auxiliaryDocStatusFromPipeline(pipeline)
	if status == rec.Status {
		return
	}
	_ = s.db.Model(&rec).Updates(s.auxiliaryDocUpdatesFromInvoice(saleID, status)).Error
}

func (s *BillingService) auxiliaryDocUpdatesFromInvoice(saleID uint, status string) map[string]interface{} {
	updates := map[string]interface{}{"status": status}
	var inv database.TenantInvoice
	if err := s.db.Where("sale_id = ?", saleID).First(&inv).Error; err == nil {
		if inv.SunatCDRCode != "" {
			updates["sunat_code"] = inv.SunatCDRCode
		}
		if inv.SunatMessage != "" {
			updates["sunat_message"] = inv.SunatMessage
		}
		if inv.CDRURL != "" {
			updates["cdr_url"] = inv.CDRURL
		}
	}
	return updates
}

func auxiliaryDocStatusFromPipeline(pipeline string) string {
	switch billingstate.NormalizePipeline(pipeline) {
	case billingstate.SUNAT_ACCEPTED, billingstate.OBSERVED:
		return "accepted"
	case billingstate.SUNAT_REJECTED:
		return "rejected"
	case billingstate.FAILED, billingstate.DEAD_LETTER, billingstate.UNKNOWN:
		return "error"
	case billingstate.PENDING_QUEUE, billingstate.PENDING_FISCAL:
		return "pending"
	case billingstate.SENDING_TO_SUNAT, billingstate.SENDING_TO_FACTURADOR, billingstate.FACTURADOR_RECEIVED, billingstate.PROCESSING, billingstate.RETRYING:
		return "sent"
	default:
		if strings.Contains(strings.ToLower(pipeline), "reject") {
			return "rejected"
		}
		return "pending"
	}
}

func despatchStatusFromPipeline(pipeline string) string {
	return auxiliaryDocStatusFromPipeline(pipeline)
}
