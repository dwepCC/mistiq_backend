package engine

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/metrics"
	"tukifac/pkg/migrationalert"
)

// FleetOptions opciones migrate-fleet.
type FleetOptions struct {
	Limit      int
	Workers    int
	ActiveOnly bool
	WorkerID   string
}

// FleetSummary resultado del lote.
type FleetSummary struct {
	Success        []string
	Failed         []database.TenantMigrateFailure
	CircuitTripped bool
	CircuitReason  string
}

// RunFleetMigrate migra tenants pendientes con workers concurrentes (no bloqueante global).
func RunFleetMigrate(opts FleetOptions) FleetSummary {
	summary := FleetSummary{}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	if opts.WorkerID == "" {
		opts.WorkerID = fmt.Sprintf("fleet-%d", time.Now().Unix())
	}

	if open, reason, err := database.IsFleetCircuitOpen(); err == nil && open {
		summary.CircuitTripped = true
		summary.CircuitReason = reason
		logger.L.Warn("fleet_migrate_skipped", slog.String("reason", "circuit_open"), slog.String("detail", reason))
		return summary
	}

	target := CodeTargetVersion()
	if _, err := BumpFleetTargetVersion(target); err != nil {
		logger.L.Warn("fleet_bump_target_failed", slog.Any("error", err))
	}
	if n := database.RecoverStaleMigrationLocks(); n > 0 {
		logger.L.Info("fleet_stale_locks_recovered", slog.Int64("count", n))
	}
	if drifted, err := ScanSchemaDriftBatch(opts.Limit); err != nil {
		logger.L.Warn("fleet_drift_scan_failed", slog.Any("error", err))
	} else if drifted > 0 {
		logger.L.Info("fleet_drift_scan_marked", slog.Int("count", drifted))
	}

	pending, err := ListPendingTenants(opts.Limit, opts.ActiveOnly)
	if err != nil {
		summary.Failed = append(summary.Failed, database.TenantMigrateFailure{
			Slug: "(list)",
			Err:  err,
		})
		return summary
	}
	if len(pending) == 0 {
		logger.L.Info("fleet_migrate_no_pending")
		return summary
	}

	cfg := config.AppConfig
	batchSize := cfg.MigrationBatchSize
	if batchSize <= 0 {
		batchSize = 50
	}
	pause := cfg.MigrationBatchPause
	lease := 10 * time.Minute

	jobs := make(chan PendingTenantRow, len(pending))
	var wg sync.WaitGroup
	var mu sync.Mutex
	circuit := &fleetCircuitTracker{}

	workerFn := func() {
		defer wg.Done()
		for row := range jobs {
			if circuit.isTripped() {
				continue
			}
			if err := MigrateFleetOne(row, opts.WorkerID, lease); err != nil {
				_ = MarkTenantFailed(row.TenantID, err)
				notifyFleetFailure(row, err)
				mu.Lock()
				summary.Failed = append(summary.Failed, database.TenantMigrateFailure{
					Slug:   row.Slug,
					DBName: row.DBName,
					Err:    err,
				})
				mu.Unlock()
				logger.L.Error("fleet_tenant_failed",
					slog.String("tenant", row.Slug),
					slog.Int("from", row.CurrentVersion),
					slog.Int("to", row.TargetVersion),
					slog.Any("error", err),
				)
				if circuit.onFailure(row.Slug, err) {
					mu.Lock()
					summary.CircuitTripped = true
					summary.CircuitReason = database.LastFleetCircuitReason()
					mu.Unlock()
					logger.L.Error("fleet_circuit_breaker_open",
						slog.Int("threshold", FleetCircuitThreshold()),
						slog.String("reason", summary.CircuitReason),
					)
				}
				continue
			}
			circuit.onSuccess()
			database.RemoveTenantFromPool(row.DBName)
			metrics.MigrationSuccessTotal.Add(1)
			mu.Lock()
			summary.Success = append(summary.Success, row.Slug)
			mu.Unlock()
		}
	}

	for w := 0; w < opts.Workers; w++ {
		wg.Add(1)
		go workerFn()
	}

	for i, row := range pending {
		jobs <- row
		if pause > 0 && batchSize > 0 && (i+1)%batchSize == 0 && i+1 < len(pending) {
			time.Sleep(pause)
		}
	}
	close(jobs)
	wg.Wait()

	if summary.CircuitTripped && summary.CircuitReason == "" {
		summary.CircuitReason = database.LastFleetCircuitReason()
	}

	logger.L.Info("fleet_migrate_done",
		slog.Int("success", len(summary.Success)),
		slog.Int("failed", len(summary.Failed)),
		slog.Bool("circuit_tripped", summary.CircuitTripped),
	)
	if len(summary.Success) > 0 {
		avg := metrics.MigrationDurationMsTotal.Load() / int64(len(summary.Success))
		database.RecordFleetRunComplete(avg)
	} else {
		database.RecordFleetRunComplete(0)
	}
	return summary
}

func notifyFleetFailure(row PendingTenantRow, err error) {
	var t database.Tenant
	name := row.Slug
	if database.CentralDB != nil {
		if e := database.CentralDB.First(&t, row.TenantID).Error; e == nil {
			name = t.Name
		}
		var attempts int
		database.CentralDB.Model(&database.TenantSchemaVersion{}).
			Where("tenant_id = ?", row.TenantID).Select("attempts").Scan(&attempts)
		migrationalert.NotifyMigrationFailure(migrationalert.TenantFailureContext{
			TenantSlug: row.Slug, TenantName: name,
			Version: row.TargetVersion, Attempts: attempts, Error: err.Error(),
		})
	}
}
