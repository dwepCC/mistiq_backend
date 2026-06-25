package service

import (
	"encoding/json"
	"fmt"
	"time"

	"tukifac/pkg/database"
)

// BulkRepairParams reparación masiva.
type BulkRepairParams struct {
	TenantIDs []uint
	Limit     int
}

// StartBulkRepairSelected inicia job en background para tenants seleccionados.
func (s *MigrationFleetService) StartBulkRepairSelected(p BulkRepairParams, saUserID uint, ip string) (*database.MigrationBatchJob, error) {
	rows, err := tenantIDsToRows(p.TenantIDs)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("sin tenants seleccionados")
	}
	return s.startMigrationJob(database.MigrationJobKindRepairSelected, len(rows), saUserID, p, func(jobID uint) {
		s.runRepairJob(jobID, rows, saUserID, ip)
	})
}

// StartBulkRepairDrifted repara tenants en estado drifted.
func (s *MigrationFleetService) StartBulkRepairDrifted(limit int, saUserID uint, ip string) (*database.MigrationBatchJob, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := listTenantsForOps("", limit, database.TenantSchemaStatusDrifted)
	if err != nil {
		return nil, err
	}
	return s.startMigrationJob(database.MigrationJobKindRepairDrifted, len(rows), saUserID, map[string]int{"limit": limit}, func(jobID uint) {
		s.runRepairJob(jobID, rows, saUserID, ip)
	})
}

// StartBulkRetryFailed reintenta tenants fallidos.
func (s *MigrationFleetService) StartBulkRetryFailed(limit int, saUserID uint, ip string) (*database.MigrationBatchJob, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := listTenantsForOps("", limit, database.TenantSchemaStatusFailed)
	if err != nil {
		return nil, err
	}
	return s.startMigrationJob(database.MigrationJobKindRetryFailed, len(rows), saUserID, map[string]int{"limit": limit}, func(jobID uint) {
		s.runRetryJob(jobID, rows, saUserID, ip)
	})
}

// StartDriftScanJob escanea drift en lote (background).
func (s *MigrationFleetService) StartDriftScanJob(limit int, saUserID uint, ip string) (*database.MigrationBatchJob, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := listTenantsForOps("", limit, "")
	if err != nil {
		return nil, err
	}
	job, err := s.startMigrationJob(database.MigrationJobKindDriftScan, len(rows), saUserID, map[string]int{"limit": limit}, func(jobID uint) {
		s.runDriftScanJob(jobID, rows, true)
	})
	if err == nil {
		logMigrationAudit(0, saUserID, "migration.drift_scan_job", "", ip)
	}
	return job, err
}

func (s *MigrationFleetService) startMigrationJob(kind string, total int, saUserID uint, payload interface{}, fn func(jobID uint)) (*database.MigrationBatchJob, error) {
	if database.CentralDB == nil {
		return nil, fmt.Errorf("BD central no conectada")
	}
	payloadJSON, _ := json.Marshal(payload)
	job := database.MigrationBatchJob{
		Kind: kind, Status: database.MigrationJobStatusPending,
		Total: total, Payload: string(payloadJSON), CreatedBy: saUserID,
	}
	if err := database.CentralDB.Create(&job).Error; err != nil {
		return nil, err
	}
	go fn(job.ID)
	return &job, nil
}

func (s *MigrationFleetService) runRepairJob(jobID uint, rows []tenantOpsRow, saUserID uint, ip string) {
	s.markJobRunning(jobID)
	results := make([]RepairResult, 0, len(rows))
	processed, succeeded, failed := 0, 0, 0
	for _, row := range rows {
		res, err := s.RepairTenant(RepairParams{TenantID: row.TenantID}, saUserID, ip)
		processed++
		if err != nil {
			failed++
			results = append(results, RepairResult{TenantID: row.TenantID, TenantSlug: row.Slug, Error: err.Error()})
		} else {
			succeeded++
			results = append(results, *res)
		}
		s.updateJobProgress(jobID, processed, succeeded, failed, encodeJobResults(results))
	}
	s.markJobCompleted(jobID, processed, succeeded, failed, encodeJobResults(results))
}

func (s *MigrationFleetService) runRetryJob(jobID uint, rows []tenantOpsRow, saUserID uint, ip string) {
	s.markJobRunning(jobID)
	results := make([]map[string]interface{}, 0, len(rows))
	processed, succeeded, failed := 0, 0, 0
	for _, row := range rows {
		processed++
		err := s.Retry(row.TenantID, saUserID, ip)
		entry := map[string]interface{}{"tenant_id": row.TenantID, "slug": row.Slug}
		if err != nil {
			failed++
			entry["error"] = err.Error()
		} else {
			succeeded++
			entry["success"] = true
		}
		results = append(results, entry)
		s.updateJobProgress(jobID, processed, succeeded, failed, encodeJobResults(results))
	}
	s.markJobCompleted(jobID, processed, succeeded, failed, encodeJobResults(results))
}

func (s *MigrationFleetService) runDriftScanJob(jobID uint, rows []tenantOpsRow, dryRun bool) {
	s.markJobRunning(jobID)
	reports := make([]TenantDriftReport, 0)
	processed, drifted := 0, 0
	for _, row := range rows {
		processed++
		report, err := s.driftReportForTenantRow(row, dryRun)
		if err == nil && report.DriftDetected {
			drifted++
			reports = append(reports, *report)
		}
		s.updateJobProgress(jobID, processed, drifted, 0, encodeJobResults(reports))
	}
	s.markJobCompleted(jobID, processed, drifted, 0, encodeJobResults(reports))
}

func (s *MigrationFleetService) markJobRunning(jobID uint) {
	now := time.Now()
	_ = database.CentralDB.Model(&database.MigrationBatchJob{}).Where("id = ?", jobID).Updates(map[string]interface{}{
		"status": database.MigrationJobStatusRunning, "started_at": now, "updated_at": now,
	}).Error
}

func (s *MigrationFleetService) updateJobProgress(jobID uint, processed, succeeded, failed int, results string) {
	_ = database.CentralDB.Model(&database.MigrationBatchJob{}).Where("id = ?", jobID).Updates(map[string]interface{}{
		"processed": processed, "succeeded": succeeded, "failed": failed,
		"results": results, "updated_at": time.Now(),
	}).Error
}

func (s *MigrationFleetService) markJobCompleted(jobID uint, processed, succeeded, failed int, results string) {
	now := time.Now()
	_ = database.CentralDB.Model(&database.MigrationBatchJob{}).Where("id = ?", jobID).Updates(map[string]interface{}{
		"status": database.MigrationJobStatusCompleted, "processed": processed,
		"succeeded": succeeded, "failed": failed, "results": results,
		"completed_at": now, "updated_at": now,
	}).Error
}
