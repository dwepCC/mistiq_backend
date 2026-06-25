package service

import (
	"errors"
	"fmt"
	"time"

	"tukifac/pkg/billingqueue"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/fiscalclient"
	"tukifac/pkg/fiscalqueue"
	"tukifac/pkg/saas/docusage"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProcessSendToSUNAT ejecuta emisión (worker de cola). Solo éxito con evidencia SUNAT real.
func (s *BillingService) ProcessSendToSUNAT(saleID uint, tenantID uint, tenantSlug string) (*database.TenantInvoice, error) {
	return s.processSendToSUNAT(saleID, tenantID, tenantSlug, FiscalSourceQueue, true)
}

func (s *BillingService) processSendToSUNAT(saleID uint, tenantID uint, tenantSlug string, source FiscalOpSource, allowResend bool) (*database.TenantInvoice, error) {
	if tenantID > 0 {
		s.centralTenantID = tenantID
	}
	if tenantSlug != "" {
		s.tenantSlug = tenantSlug
	}
	if tenantID == 0 {
		return nil, docusage.ErrTenantRequired
	}

	prep := s.PrepareFiscalOperation(saleID, tenantID, source, allowResend)
	defer prep.ReleaseLock()
	if !prep.Proceed {
		switch prep.Status {
		case "already_accepted":
			if prep.Invoice != nil {
				return prep.Invoice, errAlreadyAccepted
			}
			return nil, errAlreadyAccepted
		case "already_processing":
			if prep.Invoice != nil {
				return prep.Invoice, errAlreadyProcessing
			}
			return nil, errAlreadyProcessing
		default:
			if prep.Invoice != nil {
				return prep.Invoice, errors.New(prep.Message)
			}
			return nil, errors.New(prep.Message)
		}
	}

	if err := s.reserveSaleDocument(tenantID, saleID); err != nil {
		return nil, err
	}
	return s.executeFiscalSend(saleID)
}

func (s *BillingService) executeFiscalSend(saleID uint) (*database.TenantInvoice, error) {
	var existing database.TenantInvoice
	if err := s.db.Where("sale_id = ?", saleID).First(&existing).Error; err == nil {
		if billingstate.HasFinalSunatOutcome(&existing) {
			if billingstate.HasAcceptanceEvidence(&existing) {
				return &existing, nil
			}
		}
	}

	_ = billingstate.TransitionPipeline(s.db, saleID, billingstate.PROCESSING)

	inv, err := s.SendToSUNAT(saleID)
	if err != nil {
		if inv != nil {
			p := billingstate.NormalizePipeline(inv.PipelineStatus)
			if p == "" || p == billingstate.DRAFT {
				p = billingstate.FAILED
			}
			patch := billingstate.BuildPatch(p, inv, err.Error())
			billingstate.ApplyToInvoice(inv, patch)
			_ = s.db.Model(&database.TenantInvoice{}).Where("sale_id = ?", saleID).Updates(map[string]interface{}{
				"pipeline_status": patch.PipelineStatus,
				"sunat_status":    patch.SunatStatus,
				"sunat_message":   patch.SunatMessage,
				"job_status":      patch.JobStatus,
				"job_last_error":  patch.JobLastError,
			}).Error
			_ = billingstate.SyncSaleBillingStatus(s.db, saleID, patch.PipelineStatus)
		}
		return inv, err
	}
	s.reloadTenantInvoice(saleID, &inv)
	if inv == nil {
		return nil, errors.New("sin respuesta de facturación")
	}

	if billingstate.HasAcceptanceEvidence(inv) {
		patch := billingstate.BuildPatch(billingstate.SUNAT_ACCEPTED, inv, inv.SunatMessage)
		billingstate.ApplyToInvoice(inv, patch)
		_ = s.db.Model(&database.TenantInvoice{}).Where("sale_id = ?", saleID).Updates(map[string]interface{}{
			"pipeline_status": patch.PipelineStatus,
			"job_status":      patch.JobStatus,
			"job_last_error":  "",
		}).Error
		_ = billingstate.SyncSaleBillingStatus(s.db, saleID, billingstate.SUNAT_ACCEPTED)
		s.reloadTenantInvoice(saleID, &inv)
		return inv, nil
	}

	p := billingstate.NormalizePipeline(inv.PipelineStatus)
	if p == billingstate.SUNAT_REJECTED {
		_ = s.db.Model(&database.TenantInvoice{}).Where("sale_id = ?", saleID).Updates(map[string]interface{}{
			"job_status":     billingqueue.StatusFailed,
			"job_last_error": inv.SunatMessage,
		}).Error
		_ = billingstate.SyncSaleBillingStatus(s.db, saleID, p)
		msg := inv.SunatMessage
		if msg == "" {
			msg = "comprobante rechazado por SUNAT"
		}
		return inv, fmt.Errorf("%s", msg)
	}

	if p == billingstate.SUNAT_ACCEPTED || p == billingstate.OBSERVED {
		return inv, nil
	}

	// Encolado en facturador; intentar sync SSOT inmediato (webhook puede haber llegado ya).
	if p == billingstate.FACTURADOR_RECEIVED || p == billingstate.PENDING_FISCAL {
		var dbInv database.TenantInvoice
		if err := s.db.Where("sale_id = ?", saleID).First(&dbInv).Error; err == nil {
			if _, applied := s.fetchAndApplyFromFacturador(&dbInv, saleID); applied {
				s.reloadTenantInvoice(saleID, &inv)
				if billingstate.HasFinalSunatOutcome(inv) {
					return inv, nil
				}
			}
		}
		_ = s.db.Model(&database.TenantInvoice{}).Where("sale_id = ?", saleID).Updates(map[string]interface{}{
			"job_status":     billingqueue.StatusSent,
			"job_last_error": "",
		}).Error
		s.reloadTenantInvoice(saleID, &inv)
		return inv, nil
	}

	msg := inv.SunatMessage
	if msg == "" {
		msg = "sin confirmación SUNAT (estado " + p + ")"
	}
	_ = s.db.Model(&database.TenantInvoice{}).Where("sale_id = ?", saleID).Updates(map[string]interface{}{
		"pipeline_status": billingstate.UNKNOWN,
		"job_status":      billingqueue.StatusFailed,
		"job_last_error":  msg,
	}).Error
	_ = billingstate.SyncSaleBillingStatus(s.db, saleID, billingstate.UNKNOWN)
	return inv, fmt.Errorf("%s", msg)
}

// EnqueueSendToSUNAT encola emisión y responde rápido (idempotente por sale_id).
func (s *BillingService) EnqueueSendToSUNAT(saleID uint, tenantID uint, tenantSlug, tenantDB string, source FiscalOpSource) (*database.TenantInvoice, error) {
	if source == "" {
		source = FiscalSourceQueue
	}
	if tenantID > 0 {
		s.centralTenantID = tenantID
	}
	if tenantSlug != "" {
		s.tenantSlug = tenantSlug
	}
	if tenantID == 0 {
		return nil, docusage.ErrTenantRequired
	}

	prep := s.PrepareFiscalOperation(saleID, tenantID, source, enqueueAllowResend(source))
	defer prep.ReleaseLock()
	if !prep.Proceed {
		switch prep.Status {
		case "already_accepted":
			if prep.Invoice != nil {
				return prep.Invoice, errAlreadyAccepted
			}
			return nil, errAlreadyAccepted
		case "already_processing":
			if prep.Invoice != nil {
				return prep.Invoice, errAlreadyProcessing
			}
			return nil, errAlreadyProcessing
		default:
			if prep.Invoice != nil {
				return prep.Invoice, errors.New(prep.Message)
			}
			return nil, errors.New(prep.Message)
		}
	}

	if err := s.reserveSaleDocument(tenantID, saleID); err != nil {
		return nil, err
	}

	idemKey := billingstate.SaleIdempotencyKey(tenantDB, saleID)

	// Cola fiscal dedicada cuando está configurada.
	if fiscalqueue.Enabled() && fiscalclient.Enabled() {
		prep.ReleaseLock()
		return s.EnqueueFiscalEmit(saleID, tenantID, tenantSlug, tenantDB, idemKey)
	}

	if !billingqueue.Enabled() {
		return s.executeFiscalSend(saleID)
	}

	claimed, err := billingqueue.TryClaimEnqueue(idemKey)
	if err != nil {
		return nil, err
	}
	if !claimed {
		var existing database.TenantInvoice
		if err := s.db.Where("sale_id = ?", saleID).First(&existing).Error; err != nil {
			return nil, err
		}
		return &existing, nil
	}

	enqueued := false
	defer func() {
		if !enqueued {
			billingqueue.ReleaseClaim(idemKey)
		}
	}()

	var inv database.TenantInvoice
	err = s.db.Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("sale_id = ?", saleID).First(&inv).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			inv = database.TenantInvoice{
				SaleID:         saleID,
				JobStatus:      billingqueue.StatusPending,
				PipelineStatus: billingstate.PENDING_QUEUE,
				IdempotencyKey: idemKey,
				SunatStatus:    "pending",
			}
			return tx.Create(&inv).Error
		}
		if err != nil {
			return err
		}
		if billingstate.HasFinalSunatOutcome(&inv) && billingstate.HasAcceptanceEvidence(&inv) {
			return errAlreadyAccepted
		}
		switch inv.JobStatus {
		case billingqueue.StatusPending, billingqueue.StatusProcessing, billingqueue.StatusRetrying:
			// Borrador con payload (NC/ND recién creadas): aún no hay job en Redis.
			if billingstate.NormalizePipeline(inv.PipelineStatus) == billingstate.DRAFT {
				break
			}
			return nil
		case billingqueue.StatusSent:
			if billingstate.HasAcceptanceEvidence(&inv) {
				return errAlreadyAccepted
			}
		}
		return tx.Model(&inv).Updates(map[string]interface{}{
			"job_status":       billingqueue.StatusPending,
			"pipeline_status":  billingstate.PENDING_QUEUE,
			"idempotency_key":  idemKey,
			"job_last_error":   "",
		}).Error
	})
	if errors.Is(err, errAlreadyAccepted) {
		return &inv, errAlreadyAccepted
	}
	if err != nil {
		return nil, err
	}

	_ = billingstate.SyncSaleBillingStatus(s.db, saleID, billingstate.PENDING_QUEUE)

	prep.ReleaseLock()

	job := billingqueue.Job{
		TenantDB:       tenantDB,
		TenantID:       tenantID,
		TenantSlug:     tenantSlug,
		SaleID:         saleID,
		IdempotencyKey: idemKey,
	}
	if err := billingqueue.Enqueue(job); err != nil {
		_ = s.db.Model(&inv).Updates(map[string]interface{}{
			"job_status":      billingqueue.StatusFailed,
			"pipeline_status": billingstate.FAILED,
			"job_last_error":  err.Error(),
		}).Error
		return &inv, err
	}
	enqueued = true
	inv.JobStatus = billingqueue.StatusPending
	inv.PipelineStatus = billingstate.PENDING_QUEUE
	return &inv, nil
}

var errAlreadyAccepted = errors.New("comprobante ya enviado y aceptado por SUNAT")
var errAlreadyProcessing = errors.New("comprobante ya en proceso de emisión")

func enqueueAllowResend(source FiscalOpSource) bool {
	switch source {
	case FiscalSourceManualResend, FiscalSourceRetry, FiscalSourceReconcile, FiscalSourceFiscalQueue, FiscalSourceQueue:
		return true
	default:
		return false
	}
}

// GetBillingJobStatus devuelve estado del job para polling del frontend.
func (s *BillingService) GetBillingJobStatus(saleID uint) (*database.TenantInvoice, error) {
	var inv database.TenantInvoice
	if err := s.db.Where("sale_id = ?", saleID).First(&inv).Error; err != nil {
		return nil, err
	}
	return &inv, nil
}

// WaitForJob opcional: bloquea hasta timeout (dev/tests).
func (s *BillingService) WaitForJob(saleID uint, timeout time.Duration) (*database.TenantInvoice, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inv, err := s.GetBillingJobStatus(saleID)
		if err != nil {
			return nil, err
		}
		switch inv.JobStatus {
		case billingqueue.StatusSent, billingqueue.StatusFailed, billingqueue.StatusDeadLetter:
			return inv, nil
		}
		p := billingstate.NormalizePipeline(inv.PipelineStatus)
		if p == billingstate.SUNAT_ACCEPTED || p == billingstate.SUNAT_REJECTED || p == billingstate.FAILED {
			return inv, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return s.GetBillingJobStatus(saleID)
}

