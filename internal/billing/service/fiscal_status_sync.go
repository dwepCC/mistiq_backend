package service

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	fiscalsvc "tukifac/internal/fiscal/service"
	"tukifac/pkg/billingqueue"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/fiscaladmin"
	"tukifac/pkg/fiscalclient"
	"tukifac/pkg/fiscalqueue"
)

// ReconcileSaleFromFacturador sincroniza una venta pending antigua (worker fallback).
func (s *BillingService) ReconcileSaleFromFacturador(saleID uint) bool {
	if !fiscaladmin.Enabled() {
		return false
	}
	var sale database.TenantSale
	if err := s.db.First(&sale, saleID).Error; err != nil {
		return false
	}
	var inv database.TenantInvoice
	if err := s.db.Where("sale_id = ?", saleID).First(&inv).Error; err != nil {
		return false
	}
	// Un «rechazado» causado por una caída de SUNAT/red (o por un CDR todavía pendiente) no es definitivo: se vuelve a
	// consultar al facturador, aplicando también estados no terminales, para devolverlo a «enviado» y llegar a «aceptado».
	reopen := billingstate.LooksLikeFalseRejection(&inv)
	if !reopen {
		if sale.BillingStatus == "accepted" || sale.BillingStatus == "rejected" {
			return false
		}
		if billingstate.HasFinalSunatOutcome(&inv) {
			return false
		}
	}

	before := sale.BillingStatus
	var fiscalStatus string
	var applied bool
	if reopen {
		fiscalStatus, applied = s.fetchAndApplyFiscalDocument(&inv, saleID, false, false)
	} else {
		fiscalStatus, applied = s.fetchAndApplyFromFacturadorTerminal(&inv, saleID)
	}
	if !applied {
		return false
	}
	_ = s.db.First(&sale, saleID).Error
	if sale.BillingStatus != before && s.centralTenantID > 0 {
		NotifyBillingStatusUpdated(s.centralTenantID, saleID, inv.PipelineStatus, inv.SunatMessage)
		return true
	}
	_ = fiscalStatus
	return sale.BillingStatus != before
}

// RequeueStaleFiscalJob reencola emisiones atascadas en PENDING_FISCAL sin external_id (p. ej. race de lock).
func (s *BillingService) RequeueStaleFiscalJob(saleID uint, tenantDB string) bool {
	if !fiscalclient.Enabled() || !fiscalqueue.Enabled() {
		return false
	}
	var inv database.TenantInvoice
	if err := s.db.Where("sale_id = ?", saleID).First(&inv).Error; err != nil {
		return false
	}
	if strings.TrimSpace(inv.ExternalID) != "" {
		return false
	}
	p := billingstate.NormalizePipeline(inv.PipelineStatus)
	if p != billingstate.PENDING_FISCAL && p != billingstate.PENDING_QUEUE {
		return false
	}
	if inv.JobStatus != billingqueue.StatusPending && inv.JobStatus != fiscalqueue.StatusPending {
		return false
	}
	ref := inv.UpdatedAt
	if ref.IsZero() {
		ref = inv.CreatedAt
	}
	if time.Since(ref) < 90*time.Second {
		return false
	}
	_, err := s.EnqueueSendToSUNAT(saleID, s.centralTenantID, s.tenantSlug, tenantDB, FiscalSourceReconcile)
	return err == nil
}

// fetchAndApplyFromFacturador consulta SSOT y aplica estado (manual sync).
func (s *BillingService) fetchAndApplyFromFacturador(inv *database.TenantInvoice, saleID uint) (fiscalStatus string, applied bool) {
	return s.fetchAndApplyFiscalDocument(inv, saleID, false, false)
}

func (s *BillingService) fetchAndApplyFromFacturadorTerminal(inv *database.TenantInvoice, saleID uint) (fiscalStatus string, applied bool) {
	return s.fetchAndApplyFiscalDocument(inv, saleID, true, false)
}

// fetchAndApplyFromFacturadorForManual aplica estados terminales o errores de configuración (p. ej. certificado).
func (s *BillingService) fetchAndApplyFromFacturadorForManual(inv *database.TenantInvoice, saleID uint) (fiscalStatus string, applied bool) {
	return s.fetchAndApplyFiscalDocument(inv, saleID, false, true)
}

 

func (s *BillingService) fetchAndApplyFiscalDocument(inv *database.TenantInvoice, saleID uint, terminalOnly, manualConfigErrors bool) (fiscalStatus string, applied bool) {
	docUUID := strings.TrimSpace(inv.ExternalID)
	if docUUID == "" && s.centralTenantID > 0 {
		docUUID = s.lookupFiscalDocumentUUID(s.centralTenantID, saleID)
		if docUUID != "" {
			_ = s.db.Model(inv).Update("external_id", docUUID).Error
			inv.ExternalID = docUUID
		}
	}
	if docUUID == "" {
		return "", false
	}

	raw, _, err := fiscaladmin.GetJSON("/api/v1/fiscal/documents/"+url.PathEscape(docUUID), nil)
	if err != nil || len(raw) == 0 {
		return "", false
	}

	var detail struct {
		Document struct {
			DocumentUUID      string `json:"document_uuid"`
			TenantID          uint   `json:"tenant_id"`
			TenantSlug        string `json:"tenant_slug"`
			SaleID            uint   `json:"sale_id"`
			Status            string `json:"status"`
			SendMode          string `json:"send_mode"`
			Provider          string `json:"provider"`
			FiscalFingerprint string `json:"fiscal_fingerprint"`
			XMLURL            string `json:"xml_url"`
			XMLSignedURL      string `json:"xml_signed_url"`
			UnsignedXMLURL    string `json:"unsigned_xml_url"`
			CDRURL            string `json:"cdr_url"`
			PDFURL            string `json:"pdf_url"`
			Hash              string `json:"hash"`
			Ticket            string `json:"ticket"`
			SunatCode         string `json:"sunat_code"`
			SunatMessage      string `json:"sunat_message"`
			RetryCount        int    `json:"retry_count"`
		} `json:"document"`
	}
	if json.Unmarshal(raw, &detail) != nil {
		return "", false
	}
	d := detail.Document
	if d.DocumentUUID == "" || d.SaleID == 0 {
		return "", false
	}
	if terminalOnly && !isFiscalTerminalStatus(d.Status, d.SunatCode) {
		return "", false
	}
	if manualConfigErrors && !isFiscalTerminalStatus(d.Status, d.SunatCode) && !isFiscalConfigErrorMessage(d.SunatMessage) {
		return "", false
	}

	applyStatus := d.Status
	if manualConfigErrors && strings.EqualFold(strings.TrimSpace(d.Status), "retrying") && isFiscalConfigErrorMessage(d.SunatMessage) {
		applyStatus = "error"
	}

	sync := fiscalsvc.NewSyncService()
	if err := sync.ApplyStatus(s.db, &fiscalsvc.StatusWebhookPayload{
		TenantID:          d.TenantID,
		TenantSlug:        d.TenantSlug,
		SaleID:            d.SaleID,
		DocumentUUID:      d.DocumentUUID,
		FiscalFingerprint: d.FiscalFingerprint,
		Status:            applyStatus,
		Provider:          d.Provider,
		SendMode:          d.SendMode,
		XMLURL:            d.XMLURL,
		XMLSignedURL:      d.XMLSignedURL,
		UnsignedXMLURL:    d.UnsignedXMLURL,
		CDRURL:            d.CDRURL,
		PDFURL:            d.PDFURL,
		Hash:              d.Hash,
		Ticket:            d.Ticket,
		SunatCode:         d.SunatCode,
		SunatMessage:      d.SunatMessage,
		RetryCount:        d.RetryCount,
		EventID:           "reconcile:" + d.DocumentUUID + ":" + applyStatus + ":" + d.SunatCode,
	}); err != nil {
		return "", false
	}
	if s.centralTenantID > 0 {
		st, _ := s.GetBillingStatus(saleID)
		if st != nil {
			s.PostFiscalAcceptSideEffects(saleID, st.Pipeline)
		}
	}
	return applyStatus, true
}

func isFiscalConfigErrorMessage(msg string) bool {
	m := strings.ToLower(strings.TrimSpace(msg))
	if m == "" {
		return false
	}
	for _, needle := range []string{
		"openssl_sign",
		"openssl_pkey_get_private",
		"private key",
		"cannot be coerced",
		"certificado inválido",
		"clave privada",
	} {
		if strings.Contains(m, needle) {
			return true
		}
	}
	return false
}

func isFiscalTerminalStatus(status, sunatCode string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "accepted", "rejected", "observed", "error":
		return true
	default:
		return strings.TrimSpace(sunatCode) == "0"
	}
}

func (s *BillingService) lookupFiscalDocumentUUID(tenantID, saleID uint) string {
	q := url.Values{}
	q.Set("tenant_id", strconv.FormatUint(uint64(tenantID), 10))
	q.Set("sale_id", strconv.FormatUint(uint64(saleID), 10))
	q.Set("limit", "1")

	raw, _, err := fiscaladmin.GetJSON("/api/v1/fiscal/documents", q)
	if err != nil || len(raw) == 0 {
		return ""
	}
	var list struct {
		Items []struct {
			DocumentUUID string `json:"document_uuid"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &list) != nil || len(list.Items) == 0 {
		return ""
	}
	return strings.TrimSpace(list.Items[0].DocumentUUID)
}
