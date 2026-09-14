package service

import (
	"log/slog"
	"strings"

	prepaymentsvc "tukifac/internal/prepayment"
	salesvc "tukifac/internal/sales/service"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"

	"gorm.io/gorm"
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
	_ = prepaymentsvc.NewService(s.db).OnFiscalAccept(saleID)

	if sale.DocType != "NOTA_CREDITO" || sale.OriginalSaleID == nil {
		return
	}
	// Solo el motivo "01" (anulación de la operación) implica anular la venta completa y
	// restaurar el 100% del stock — antes era el único motivo que el sistema podía emitir,
	// así que este gate no cambia nada para notas ya en curso (reasonCode vacío = "01").
	if reasonCode := strings.TrimSpace(sale.NoteReasonCode); reasonCode != "" && reasonCode != "01" {
		// Motivo que mueve bienes (descuento, devolución, etc. — Fase 2): repone stock solo
		// de lo que la nota devuelve, sin anular la venta original. Motivo que no mueve bienes
		// (corrección de RUC/descripción, ajustes de exportación/IVAP/fecha de pago): la nota
		// queda registrada sin ningún efecto sobre venta o stock.
		if IsPartialCreditNoteReason(reasonCode) {
			s.applyPartialCreditNoteSideEffects(saleID, &sale)
		}
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

	// Si `orig` había deducido anticipos, repone el saldo de los vouchers origen y marca esas
	// aplicaciones como revertidas — si no, esos anticipos quedaban con saldo reducido para
	// siempre y no volvían a aparecer en la lista de anticipos disponibles.
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		return prepaymentsvc.NewService(tx).ReverseApplicationsForConsumerSaleTx(tx, origID)
	}); err != nil {
		logger.L.Warn("nc_void_reverse_prepayment_applications_failed",
			slog.Uint64("tenant_id", uint64(s.centralTenantID)),
			slog.Uint64("nc_sale_id", uint64(saleID)),
			slog.Uint64("original_sale_id", uint64(origID)),
			slog.Any("error", err),
		)
	}
}

// applyPartialCreditNoteSideEffects repone el stock que corresponde a una nota de crédito
// parcial (Fase 2) — solo lo que la nota devuelve, no toda la venta — y deja registrado el
// monto de la nota como devolución pendiente (ver sale_pending_refunds.go), sin anular la
// venta original ni tocar anticipos: esos efectos son propios de la anulación total (motivo
// "01") y no aplican a un descuento o devolución parcial.
func (s *BillingService) applyPartialCreditNoteSideEffects(saleID uint, sale *database.TenantSale) {
	var inv database.TenantInvoice
	if err := s.db.Where("sale_id = ?", saleID).First(&inv).Error; err != nil {
		return
	}
	if !billingstate.HasAcceptanceEvidence(&inv) {
		if inv.SunatStatus != "accepted" && inv.SunatCDRCode != "0" {
			return
		}
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		return salesvc.RestorePartialStockFromKardexTx(tx, sale, "NC/"+sale.Number, 0)
	}); err != nil {
		logger.L.Warn("nc_partial_restore_stock_failed",
			slog.Uint64("tenant_id", uint64(s.centralTenantID)),
			slog.Uint64("nc_sale_id", uint64(saleID)),
			slog.Any("error", err),
		)
		return
	}
	logger.L.Info("nc_partial_restore_stock_ok",
		slog.Uint64("tenant_id", uint64(s.centralTenantID)),
		slog.Uint64("nc_sale_id", uint64(saleID)),
	)

	// Devuelve el dinero en Cuentas/Bancos si la venta original se cobró ahí (independiente del
	// resultado de la reposición de stock arriba: son efectos separados, uno no debe bloquear
	// al otro). Si se cobró en efectivo, no hace nada acá — ese caso lo cubre la devolución
	// pendiente manual de Caja (sale_pending_refunds.go).
	origID := uint(0)
	if sale.OriginalSaleID != nil {
		origID = *sale.OriginalSaleID
	}
	var origNumber string
	if origID > 0 {
		var orig database.TenantSale
		if s.db.Select("number").First(&orig, origID).Error == nil {
			origNumber = orig.Number
		}
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		return salesvc.RestorePartialSaleBankRefundTx(tx, sale, origID, origNumber, 0)
	}); err != nil {
		logger.L.Warn("nc_partial_bank_refund_failed",
			slog.Uint64("tenant_id", uint64(s.centralTenantID)),
			slog.Uint64("nc_sale_id", uint64(saleID)),
			slog.Any("error", err),
		)
	}
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
