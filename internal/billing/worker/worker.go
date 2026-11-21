package worker

import (
	"log/slog"
	"tukifac/pkg/branchscope"

	billingsvc "tukifac/internal/billing/service"
	"tukifac/pkg/billingqueue"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"

	"gorm.io/gorm"
)

// ProcessJob ejecuta emisión SUNAT para un job de cola.
func ProcessJob(job billingqueue.Job) error {
	db, err := database.GetTenantDB(job.TenantDB)
	if err != nil {
		return err
	}
	defer database.ReleaseTenantDB(job.TenantDB)
	db = branchscope.System(db, "billing worker: procesamiento de job")

	updateJobStatus(db, job.SaleID, billingqueue.StatusProcessing, billingstate.PROCESSING)

	svc := billingsvc.NewBillingService(db)
	tenantID := job.TenantID
	if tenantID == 0 {
		tenantID = billingsvc.TenantIDFromDB(job.TenantDB)
	}
	_, err = svc.ProcessSendToSUNAT(job.SaleID, tenantID, job.TenantSlug)
	if err == nil {
		return nil
	}
	// No reintentar si SUNAT ya respondió (evita doble emisión en requeue).
	var inv database.TenantInvoice
	if e := db.Where("sale_id = ?", job.SaleID).First(&inv).Error; e == nil {
		if billingstate.HasFinalSunatOutcome(&inv) {
			return nil
		}
	}
	return err
}

func updateJobStatus(db *gorm.DB, saleID uint, jobStatus, pipeline string) {
	updates := map[string]interface{}{
		"job_status":      jobStatus,
		"pipeline_status": pipeline,
	}
	if err := db.Model(&database.TenantInvoice{}).Where("sale_id = ?", saleID).Updates(updates).Error; err != nil {
		logger.L.Warn("billing_job_status_update_failed",
			slog.Uint64("sale_id", uint64(saleID)),
			slog.Any("error", err),
		)
	}
}
