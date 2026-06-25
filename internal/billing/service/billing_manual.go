package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

const manualFiscalWaitTimeout = 90 * time.Second

// ManualBillingResult respuesta unificada para send/resend manual.
type ManualBillingResult struct {
	Status        string                   `json:"status"` // accepted, rejected, error, processing, already_accepted, queued, already_processing
	Message       string                   `json:"message"`
	BillingStatus string                   `json:"billing_status"`
	SafeToPrint   bool                     `json:"safe_to_print"`
	SunatMessage  string                   `json:"sunat_message,omitempty"`
	StatusDetail  *billingstate.StatusView `json:"status_detail,omitempty"`
	Invoice       *database.TenantInvoice    `json:"invoice,omitempty"`
	Success       bool                     `json:"success"`
	Async         bool                     `json:"async"`
}

// ManualSendToSUNAT emite de forma síncrona (espera respuesta del facturador/SUNAT).
func (s *BillingService) ManualSendToSUNAT(saleID, tenantID uint, tenantSlug string) (*ManualBillingResult, error) {
	if tenantID > 0 {
		s.centralTenantID = tenantID
	}
	if tenantSlug != "" {
		s.tenantSlug = tenantSlug
	}
	if tenantID == 0 {
		return nil, errors.New("tenant requerido")
	}

	inv, err := s.processSendToSUNAT(saleID, tenantID, tenantSlug, FiscalSourceManual, true)
	if err != nil {
		return s.buildManualResultFromSend(saleID, inv, err)
	}
	inv, err = s.waitForFacturadorOutcome(saleID, manualFiscalWaitTimeout)
	return s.buildManualResultFromSend(saleID, inv, err)
}

// ManualResendToSUNAT sincroniza SSOT y reenvía de forma síncrona.
func (s *BillingService) ManualResendToSUNAT(saleID uint) (*ManualBillingResult, error) {
	outcome := s.SyncSaleWithSSOT(saleID)
	switch outcome.ManualStatus {
	case "already_accepted", "accepted":
		outcome.ManualStatus = "already_accepted"
		outcome.Message = "El comprobante ya fue aceptado por SUNAT"
		outcome.Async = false
		return s.buildManualResult(saleID, outcome.Invoice, outcome, nil), nil
	}

	var inv database.TenantInvoice
	err := s.db.Where("sale_id = ?", saleID).First(&inv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		tenantID := s.centralTenantID
		tenantSlug := s.tenantSlug
		inv2, sendErr := s.processSendToSUNAT(saleID, tenantID, tenantSlug, FiscalSourceManualResend, true)
		if sendErr != nil {
			return s.buildManualResultFromSend(saleID, inv2, sendErr)
		}
		inv2, sendErr = s.waitForFacturadorOutcome(saleID, manualFiscalWaitTimeout)
		return s.buildManualResultFromSend(saleID, inv2, sendErr)
	}
	if err != nil {
		return nil, errors.New("comprobante no encontrado")
	}

	if billingstate.HasAcceptanceEvidence(&inv) || outcome.BillingStatus == "accepted" {
		synced := s.SyncSaleWithSSOT(saleID)
		synced.ManualStatus = "already_accepted"
		synced.Message = "El comprobante ya fue aceptado por SUNAT"
		synced.Async = false
		return s.buildManualResult(saleID, &inv, synced, nil), nil
	}

	tenantID := s.centralTenantID
	tenantSlug := s.tenantSlug

	inv2, err := s.processSendToSUNAT(saleID, tenantID, tenantSlug, FiscalSourceManualResend, true)
	if err != nil {
		return s.buildManualResultFromSend(saleID, inv2, err)
	}
	inv2, err = s.waitForFacturadorOutcome(saleID, manualFiscalWaitTimeout)
	return s.buildManualResultFromSend(saleID, inv2, err)
}

func (s *BillingService) buildManualResultFromSend(saleID uint, inv *database.TenantInvoice, procErr error) (*ManualBillingResult, error) {
	if errors.Is(procErr, errAlreadyAccepted) {
		synced := s.SyncSaleWithSSOT(saleID)
		synced.ManualStatus = "already_accepted"
		synced.Message = "El comprobante ya fue aceptado por SUNAT"
		synced.Async = false
		return s.buildManualResult(saleID, inv, synced, nil), nil
	}

	st, _ := s.GetBillingStatus(saleID)
	o := s.outcomeFromStatusView(st, "")
	o.Async = false
	if inv != nil {
		o.Invoice = inv
	}
	if procErr != nil {
		o.Message = procErr.Error()
		switch billingstate.NormalizePipeline(st.Pipeline) {
		case billingstate.SUNAT_REJECTED:
			o.ManualStatus = "rejected"
		case billingstate.FAILED, billingstate.DEAD_LETTER, billingstate.UNKNOWN:
			o.ManualStatus = "error"
		default:
			if o.ManualStatus == "pending" || o.ManualStatus == "processing" || o.ManualStatus == "queued" {
				o.ManualStatus = "error"
			}
		}
	}
	return s.buildManualResult(saleID, inv, o, procErr), nil
}

func (s *BillingService) waitForFacturadorOutcome(saleID uint, timeout time.Duration) (*database.TenantInvoice, error) {
	deadline := time.Now().Add(timeout)
	backoff := []time.Duration{
		400 * time.Millisecond,
		800 * time.Millisecond,
		1500 * time.Millisecond,
		2500 * time.Millisecond,
		4000 * time.Millisecond,
	}
	attempt := 0

	for time.Now().Before(deadline) {
		var inv database.TenantInvoice
		if err := s.db.Where("sale_id = ?", saleID).First(&inv).Error; err != nil {
			return nil, err
		}
		if billingstate.HasFinalSunatOutcome(&inv) {
			if p := billingstate.NormalizePipeline(inv.PipelineStatus); p == billingstate.SUNAT_REJECTED {
				msg := inv.SunatMessage
				if msg == "" {
					msg = "comprobante rechazado por SUNAT"
				}
				return &inv, fmt.Errorf("%s", msg)
			}
			return &inv, nil
		}

		if strings.TrimSpace(inv.ExternalID) != "" || s.centralTenantID > 0 {
			if _, applied := s.fetchAndApplyFromFacturadorForManual(&inv, saleID); applied {
				_ = s.db.Where("sale_id = ?", saleID).First(&inv).Error
				if billingstate.HasFinalSunatOutcome(&inv) {
					if s.centralTenantID > 0 {
						NotifyBillingStatusUpdated(s.centralTenantID, saleID, inv.PipelineStatus, inv.SunatMessage)
					}
					if p := billingstate.NormalizePipeline(inv.PipelineStatus); p == billingstate.SUNAT_REJECTED {
						msg := inv.SunatMessage
						if msg == "" {
							msg = "comprobante rechazado por SUNAT"
						}
						return &inv, fmt.Errorf("%s", msg)
					}
					return &inv, nil
				}
			}
		}

		wait := backoff[len(backoff)-1]
		if attempt < len(backoff) {
			wait = backoff[attempt]
		}
		attempt++
		time.Sleep(wait)
	}

	var inv database.TenantInvoice
	if err := s.db.Where("sale_id = ?", saleID).First(&inv).Error; err != nil {
		return nil, fmt.Errorf("tiempo de espera agotado; el comprobante sigue en proceso")
	}
	return &inv, fmt.Errorf("tiempo de espera agotado; el comprobante sigue en proceso")
}

func (s *BillingService) lookupTenantDBName(tenantID uint) string {
	if tenantID == 0 {
		return ""
	}
	var t database.Tenant
	if database.CentralDB.Select("db_name").First(&t, tenantID).Error != nil {
		return ""
	}
	return t.DBName
}

// SSOTSyncOutcome resultado de sincronizar una venta con el facturador.
type SSOTSyncOutcome struct {
	ManualStatus  string
	Message       string
	BillingStatus string
	FiscalStatus  string
	StatusView    *billingstate.StatusView
	Invoice       *database.TenantInvoice
	Synced        bool
	Async         bool
}

func (s *BillingService) SyncSaleWithSSOT(saleID uint) SSOTSyncOutcome {
	var inv database.TenantInvoice
	err := s.db.Where("sale_id = ?", saleID).First(&inv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		st, _ := s.GetBillingStatus(saleID)
		return s.outcomeFromStatusView(st, "")
	}
	if err != nil {
		return SSOTSyncOutcome{ManualStatus: "error", Message: err.Error()}
	}

	before := inv.PipelineStatus
	var sale database.TenantSale
	_ = s.db.First(&sale, saleID).Error
	beforeBilling := sale.BillingStatus

	fiscalStatus, applied := s.fetchAndApplyFromFacturador(&inv, saleID)
	if applied && s.centralTenantID > 0 {
		_ = s.db.Where("sale_id = ?", saleID).First(&inv).Error
		if inv.PipelineStatus != before {
			NotifyBillingStatusUpdated(s.centralTenantID, saleID, inv.PipelineStatus, inv.SunatMessage)
		}
		if st, _ := s.GetBillingStatus(saleID); st != nil {
			s.PostFiscalAcceptSideEffects(saleID, st.Pipeline)
		}
	}

	st, _ := s.GetBillingStatus(saleID)
	out := s.outcomeFromStatusView(st, fiscalStatus)
	out.Synced = applied && (st.BillingStatus != beforeBilling || inv.PipelineStatus != before)
	out.Invoice = &inv
	return out
}

func (s *BillingService) buildManualResult(saleID uint, inv *database.TenantInvoice, outcome SSOTSyncOutcome, procErr error) *ManualBillingResult {
	if outcome.StatusView == nil {
		st, _ := s.GetBillingStatus(saleID)
		outcome = s.outcomeFromStatusView(st, outcome.FiscalStatus)
	}
	if inv == nil && outcome.Invoice != nil {
		inv = outcome.Invoice
	}
	if inv == nil {
		var dbInv database.TenantInvoice
		if err := s.db.Where("sale_id = ?", saleID).First(&dbInv).Error; err == nil {
			inv = &dbInv
		}
	}

	ms := outcome.ManualStatus
	if ms == "" {
		ms = manualStatusFromView(outcome.StatusView)
	}
	msg := outcome.Message
	if msg == "" {
		msg = manualMessage(ms, outcome.StatusView, procErr)
	}

	res := &ManualBillingResult{
		Status:        ms,
		Message:       msg,
		BillingStatus: manualResultBillingStatus(ms, outcome),
		SafeToPrint:   outcome.StatusView != nil && outcome.StatusView.SafeToPrint,
		SunatMessage:  "",
		StatusDetail:  outcome.StatusView,
		Invoice:       inv,
		Success:       ms == "accepted" || ms == "already_accepted",
		Async:         outcome.Async || ms == "processing" || ms == "queued" || ms == "already_processing",
	}
	if outcome.StatusView != nil {
		res.SunatMessage = outcome.StatusView.SunatMessage
	}
	return res
}

func manualResultBillingStatus(ms string, outcome SSOTSyncOutcome) string {
	switch ms {
	case "accepted", "already_accepted":
		return billingstate.BillingAccepted
	case "rejected":
		return billingstate.BillingRejected
	case "error":
		return billingstate.BillingError
	case "processing", "queued", "already_processing":
		if outcome.StatusView != nil {
			if bs := billingstate.NormalizeBillingStatus(outcome.StatusView.BillingStatus); bs != billingstate.BillingPending {
				return bs
			}
			return billingstate.LegacyBillingStatus(outcome.StatusView.Pipeline)
		}
		return billingstate.BillingSent
	default:
		if outcome.StatusView != nil {
			return billingstate.NormalizeBillingStatus(outcome.StatusView.BillingStatus)
		}
		return billingstate.BillingPending
	}
}

func (s *BillingService) outcomeFromStatusView(st *billingstate.StatusView, fiscalStatus string) SSOTSyncOutcome {
	if st == nil {
		return SSOTSyncOutcome{ManualStatus: "error", Message: "estado no disponible"}
	}
	ms := manualStatusFromView(st)
	if fiscalStatus == "accepted" && ms == "pending" {
		ms = "accepted"
	}
	return SSOTSyncOutcome{
		ManualStatus:  ms,
		BillingStatus: st.BillingStatus,
		FiscalStatus:  fiscalStatus,
		StatusView:    st,
		Message:       manualMessage(ms, st, nil),
	}
}

func (s *BillingService) outcomeFromError(saleID uint, inv *database.TenantInvoice, err error) SSOTSyncOutcome {
	st, _ := s.GetBillingStatus(saleID)
	o := s.outcomeFromStatusView(st, "")
	o.Invoice = inv
	if err != nil {
		o.Message = err.Error()
		if o.ManualStatus == "pending" || o.ManualStatus == "processing" {
			o.ManualStatus = "error"
		}
	}
	return o
}

func manualStatusFromView(st *billingstate.StatusView) string {
	if st == nil {
		return "error"
	}
	if st.DisplayPhase != "" {
		switch st.DisplayPhase {
		case billingstate.PhaseAccepted, billingstate.PhaseObserved:
			return "accepted"
		case billingstate.PhaseRejected:
			return "rejected"
		case billingstate.PhaseError:
			return "error"
		case billingstate.PhaseQueued:
			return "queued"
		case billingstate.PhaseSending, billingstate.PhaseRetrying:
			return "processing"
		case billingstate.PhasePending, billingstate.PhaseDraft:
			return "pending"
		}
	}
	switch st.BillingStatus {
	case "accepted":
		return "accepted"
	case "rejected":
		return "rejected"
	case "error":
		return "error"
	case "sent":
		return "processing"
	}
	p := billingstate.NormalizePipeline(st.Pipeline)
	switch p {
	case billingstate.SUNAT_ACCEPTED, billingstate.OBSERVED:
		return "accepted"
	case billingstate.SUNAT_REJECTED:
		return "rejected"
	case billingstate.FAILED, billingstate.DEAD_LETTER:
		return "error"
	case billingstate.PENDING_QUEUE, billingstate.PENDING_FISCAL,
		billingstate.PROCESSING, billingstate.SENDING_TO_FACTURADOR,
		billingstate.FACTURADOR_RECEIVED, billingstate.SENDING_TO_SUNAT,
		billingstate.RETRYING:
		return "processing"
	}
	if st.Async {
		return "processing"
	}
	if st.BillingStatus == "pending" {
		return "pending"
	}
	return "pending"
}

func manualMessage(status string, st *billingstate.StatusView, err error) string {
	if err != nil && status == "error" {
		return err.Error()
	}
	if st != nil && st.SunatMessage != "" && (status == "rejected" || status == "error") {
		return st.SunatMessage
	}
	switch status {
	case "accepted":
		return "Comprobante aceptado por SUNAT"
	case "already_accepted":
		return "El comprobante ya fue aceptado por SUNAT"
	case "rejected":
		return "Comprobante rechazado por SUNAT"
	case "error":
		return "Error al enviar el comprobante"
	case "queued":
		return "Comprobante en cola de emisión"
	case "already_processing":
		return "El comprobante ya está en proceso de emisión"
	case "processing":
		return "Comprobante en proceso; recibirá la actualización en tiempo real"
	default:
		return "Operación completada"
	}
}
