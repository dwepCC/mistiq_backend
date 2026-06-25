package worker

import (
	"fmt"
	"log/slog"
	"time"

	billingsvc "tukifac/internal/billing/service"
	"tukifac/pkg/billinglock"
	"tukifac/pkg/billingqueue"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/fiscalqueue"
	"tukifac/pkg/logger"
	"tukifac/pkg/metrics"
)

// ProcessJob construye snapshot y encola en facturador_lycet (sin SUNAT en ERP).
func ProcessJob(job fiscalqueue.Job) error {
	db, err := database.GetTenantDB(job.TenantDB)
	if err != nil {
		return err
	}
	defer database.ReleaseTenantDB(job.TenantDB)

	svc := billingsvc.NewBillingService(db)
	tenantID := job.TenantID
	if tenantID == 0 {
		tenantID = billingsvc.TenantIDFromDB(job.TenantDB)
	}
	tenantSlug := billingsvc.ResolveTenantSlug(tenantID, job.TenantDB, job.TenantSlug)
	if tenantID == 0 || tenantSlug == "" {
		return fmt.Errorf("fiscal queue: tenant_id/slug requeridos (db=%s sale=%d)", job.TenantDB, job.SaleID)
	}
	svc.SetCentralTenantID(tenantID)
	svc.SetTenantSlug(tenantSlug)

	prep := svc.PrepareFiscalOperation(job.SaleID, tenantID, billingsvc.FiscalSourceFiscalQueue, true)
	defer prep.ReleaseLock()
	if !prep.Proceed {
		if prep.Status == "already_accepted" {
			metrics.FiscalQueueProcessed.Add(1)
			return nil
		}
		if prep.Status == "already_processing" {
			scheduleFiscalRetry(job)
			return nil
		}
		if prep.Message != "" {
			return fmt.Errorf("fiscal queue: %s", prep.Message)
		}
		return nil
	}
	billinglock.Extend(tenantID, job.SaleID, string(billingsvc.FiscalSourceFiscalQueue), billinglock.DefaultTTL)

	_, err = svc.EmitFiscal(job.SaleID)
	if err != nil {
		errMsg := err.Error()
		logger.L.Error("fiscal_queue_emit_failed",
			slog.Uint64("tenant_id", uint64(tenantID)),
			slog.String("tenant_slug", tenantSlug),
			slog.Uint64("sale_id", uint64(job.SaleID)),
			slog.Any("error", err),
		)
		_ = db.Model(&database.TenantInvoice{}).Where("sale_id = ?", job.SaleID).Updates(map[string]interface{}{
			"job_status":      billingqueue.StatusFailed,
			"pipeline_status": billingstate.FAILED,
			"job_last_error":  errMsg,
			"sunat_message":   errMsg,
		}).Error
		_ = billingstate.SyncSaleBillingStatus(db, job.SaleID, billingstate.FAILED)
		billingsvc.NotifyBillingStatusUpdated(tenantID, job.SaleID, billingstate.FAILED, errMsg)
		return err
	}
	metrics.FiscalQueueProcessed.Add(1)
	logger.L.Info("fiscal_queue_emit_ok",
		slog.Uint64("tenant_id", uint64(tenantID)),
		slog.String("tenant_slug", tenantSlug),
		slog.Uint64("sale_id", uint64(job.SaleID)),
	)
	return nil
}

func scheduleFiscalRetry(job fiscalqueue.Job) {
	time.AfterFunc(2*time.Second, func() {
		if err := fiscalqueue.Enqueue(job); err != nil {
			logger.L.Error("fiscal_queue_retry_enqueue_failed",
				slog.Uint64("tenant_id", uint64(job.TenantID)),
				slog.Uint64("sale_id", uint64(job.SaleID)),
				slog.Any("error", err),
			)
		}
	})
}
