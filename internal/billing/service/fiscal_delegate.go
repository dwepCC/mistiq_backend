package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	"tukifac/pkg/fiscalclient"

	"gorm.io/gorm"
)

func requireFiscalClient() error {
	if !fiscalclient.Enabled() {
		return errors.New("facturador fiscal no configurado: FACTURADOR_BASE_URL y FACTURADOR_TOKEN requeridos")
	}
	return nil
}

// enqueueFiscalMicroservice encola documento en facturador (SSOT). Payload: tenant_id + ruc + document.
func (s *BillingService) enqueueFiscalMicroservice(
	saleID uint,
	companyCfg *database.TenantCompanyConfig,
	payload *facturador.InvoicePayload,
	payloadJSON string,
) (*database.TenantInvoice, error) {
	if err := requireFiscalClient(); err != nil {
		return nil, err
	}
	if s.centralTenantID == 0 || s.tenantSlug == "" {
		return nil, errors.New("contexto tenant SaaS requerido para emisión fiscal")
	}

	var document map[string]interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &document); err != nil {
		return nil, fmt.Errorf("document: %w", err)
	}
	delete(document, "_meta")

	ruc := strings.TrimSpace(companyCfg.RUC)
	if ruc == "" && payload != nil {
		ruc = strings.TrimSpace(payload.Company.RUC)
	}
	if ruc == "" {
		return nil, errors.New("ruc de empresa requerido para emisión fiscal")
	}

	fingerprint := fmt.Sprintf("%d|%s|%s|%s|%d",
		s.centralTenantID,
		document["tipoDoc"],
		document["serie"],
		document["correlativo"],
		saleID,
	)

	resp, err := fiscalclient.Emit(&fiscalclient.EmitRequest{
		TenantID:       s.centralTenantID,
		TenantSlug:     s.tenantSlug,
		SaleID:         saleID,
		RUC:            ruc,
		Document:       document,
		IdempotencyKey: fingerprint,
	})
	if err != nil {
		return nil, err
	}

	// El webhook puede aplicar accepted antes de que retorne HTTP emit.
	return s.persistInvoiceAfterEmit(saleID, resp.DocumentUUID, payloadJSON)
}

func (s *BillingService) persistInvoiceAfterEmit(saleID uint, documentUUID, payloadJSON string) (*database.TenantInvoice, error) {
	now := time.Now()
	invoice, err := s.loadOrCreateTenantInvoice(saleID)
	if err != nil {
		return nil, err
	}
	_ = s.db.Where("sale_id = ?", saleID).First(invoice).Error

	if billingstate.HasFinalSunatOutcome(invoice) {
		return s.patchInvoiceMetadata(invoice, documentUUID, payloadJSON)
	}

	updates := map[string]interface{}{
		"external_id":     documentUUID,
		"payload_json":    payloadJSON,
		"pipeline_status": billingstate.FACTURADOR_RECEIVED,
		"job_status":      "sent",
		"sunat_status":    "pending",
		"sent_at":         now,
	}
	var sale database.TenantSale
	if s.db.Select("doc_type").First(&sale, saleID).Error == nil && isNoteSaleDocType(sale.DocType) {
		updates["note_payload_json"] = payloadJSON
	}
	if err := s.db.Model(&database.TenantInvoice{}).Where("id = ?", invoice.ID).Updates(updates).Error; err != nil {
		return nil, err
	}

	if err := s.db.Where("id = ?", invoice.ID).First(invoice).Error; err != nil {
		return nil, err
	}

	if !billingstate.HasFinalSunatOutcome(invoice) {
		if err := s.db.Select("billing_status").First(&sale, saleID).Error; err == nil {
			switch sale.BillingStatus {
			case "accepted", "rejected":
				// no degradar estado ya final en tenant_sales
			default:
				_ = billingstate.SyncSaleBillingStatus(s.db, saleID, billingstate.PENDING_FISCAL)
			}
		}
	}

	return invoice, nil
}

func (s *BillingService) loadOrCreateTenantInvoice(saleID uint) (*database.TenantInvoice, error) {
	var invoice database.TenantInvoice
	err := s.db.Where("sale_id = ?", saleID).First(&invoice).Error
	if err == nil {
		return &invoice, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	invoice = database.TenantInvoice{
		SaleID:         saleID,
		PipelineStatus: billingstate.PENDING_FISCAL,
		JobStatus:      "pending",
		SunatStatus:    "pending",
	}
	if createErr := s.db.Create(&invoice).Error; createErr != nil {
		if reloadErr := s.db.Where("sale_id = ?", saleID).First(&invoice).Error; reloadErr != nil {
			return nil, fmt.Errorf("tenant_invoices upsert sale_id=%d: %w", saleID, createErr)
		}
	}
	return &invoice, nil
}

func (s *BillingService) patchInvoiceMetadata(inv *database.TenantInvoice, documentUUID, payloadJSON string) (*database.TenantInvoice, error) {
	if inv == nil || inv.ID == 0 {
		return inv, nil
	}
	patches := map[string]interface{}{}
	if strings.TrimSpace(inv.ExternalID) == "" && strings.TrimSpace(documentUUID) != "" {
		patches["external_id"] = documentUUID
	}
	if strings.TrimSpace(inv.PayloadJSON) == "" && strings.TrimSpace(payloadJSON) != "" {
		patches["payload_json"] = payloadJSON
	}
	if len(patches) > 0 {
		_ = s.db.Model(&database.TenantInvoice{}).Where("id = ?", inv.ID).Updates(patches).Error
		_ = s.db.Where("id = ?", inv.ID).First(inv).Error
	}
	return inv, nil
}

func (s *BillingService) reloadTenantInvoice(saleID uint, inv **database.TenantInvoice) {
	if inv == nil {
		return
	}
	var loaded database.TenantInvoice
	if err := s.db.Where("sale_id = ?", saleID).First(&loaded).Error; err == nil {
		*inv = &loaded
	}
}
