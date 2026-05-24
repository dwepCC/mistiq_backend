package engine

import (
	"log/slog"
	"sync"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantbackfills"
	"tukifac/pkg/logger"
)

// BackfillOptions migrate-backfill-fleet.
type BackfillOptions struct {
	Limit      int
	Workers    int
	ActiveOnly bool
	Version    int
	TenantSlug string
}

// RunBackfillFleet ejecuta backfills run-once registrados.
// Version <= 0: recorre todos los backfills del registry (recomendado en cron).
// Version > 0: un backfill concreto; si no existe en registry, skip operacional (no falla el proceso).
func RunBackfillFleet(opts BackfillOptions) database.MigrateSummary {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.Version <= 0 {
		return runAllRegisteredBackfills(opts)
	}

	byVer := tenantbackfills.ByVersion()
	bf, ok := byVer[opts.Version]
	if !ok {
		logger.L.Warn("backfill_registry_skip",
			slog.Int("version", opts.Version),
			slog.String("reason", "not_registered"),
		)
		return database.MigrateSummary{}
	}
	return runBackfillFleetForVersion(opts, bf)
}

func runAllRegisteredBackfills(opts BackfillOptions) database.MigrateSummary {
	merged := database.MigrateSummary{}
	budget := opts.Limit
	for _, reg := range tenantbackfills.TenantBackfills {
		if opts.Limit > 0 && budget <= 0 {
			break
		}
		sub := opts
		sub.Version = reg.Version()
		if opts.Limit > 0 {
			sub.Limit = budget
		}
		part := runBackfillFleetForVersion(sub, reg)
		merged.Success = append(merged.Success, part.Success...)
		merged.Failed = append(merged.Failed, part.Failed...)
		if opts.Limit > 0 {
			budget -= len(part.Success) + len(part.Failed)
		}
	}
	return merged
}

func runBackfillFleetForVersion(opts BackfillOptions, bf tenantbackfills.TenantBackfill) database.MigrateSummary {
	summary := database.MigrateSummary{}
	version := bf.Version()

	var tenants []database.Tenant
	var err error
	if opts.TenantSlug != "" {
		var t database.Tenant
		if err := database.CentralDB.Where("slug = ?", opts.TenantSlug).First(&t).Error; err != nil {
			summary.Failed = append(summary.Failed, database.TenantMigrateFailure{Slug: opts.TenantSlug, Err: err})
			return summary
		}
		tenants = []database.Tenant{t}
	} else {
		tenants, err = database.ListTenantsForMigration(opts.ActiveOnly)
		if err != nil {
			summary.Failed = append(summary.Failed, database.TenantMigrateFailure{Slug: "(list)", Err: err})
			return summary
		}
		if opts.Limit > 0 && len(tenants) > opts.Limit {
			tenants = tenants[:opts.Limit]
		}
	}

	cfg := config.AppConfig
	pause := cfg.MigrationBatchPause
	batchSize := cfg.MigrationBatchSize
	if batchSize <= 0 {
		batchSize = 50
	}

	jobs := make(chan database.Tenant, len(tenants))
	var wg sync.WaitGroup
	var mu sync.Mutex

	worker := func() {
		defer wg.Done()
		for t := range jobs {
			if err := runBackfillOne(t.Slug, t.DBName, version, bf); err != nil {
				mu.Lock()
				summary.Failed = append(summary.Failed, database.TenantMigrateFailure{
					Slug: t.Slug, DBName: t.DBName, Err: err,
				})
				mu.Unlock()
				continue
			}
			mu.Lock()
			summary.Success = append(summary.Success, t.Slug)
			mu.Unlock()
		}
	}
	for w := 0; w < opts.Workers; w++ {
		wg.Add(1)
		go worker()
	}
	for i, t := range tenants {
		jobs <- t
		if pause > 0 && batchSize > 0 && (i+1)%batchSize == 0 && i+1 < len(tenants) {
			time.Sleep(pause)
		}
	}
	close(jobs)
	wg.Wait()
	return summary
}

func runBackfillOne(slug, dbName string, version int, bf tenantbackfills.TenantBackfill) error {
	db, err := database.OpenTenantDBForMigration(dbName)
	if err != nil {
		return err
	}
	defer database.CloseTenantDB(db)

	applied, err := IsBackfillApplied(db, version)
	if err != nil {
		return err
	}
	if applied {
		logger.L.Info("tenant_backfill_skip",
			slog.String("tenant", slug),
			slog.Int("version", version),
		)
		return nil
	}

	logger.L.Info("tenant_backfill_start",
		slog.String("tenant", slug),
		slog.Int("version", version),
		slog.String("name", bf.Name()),
	)
	start := time.Now()
	err = bf.Run(db)
	dur := time.Since(start)
	if err != nil {
		_ = RecordBackfillHistory(db, version, bf.Name(), dur, false, err.Error())
		logger.L.Error("tenant_backfill_failed",
			slog.String("tenant", slug),
			slog.Duration("duration", dur),
			slog.Any("error", err),
		)
		return err
	}
	if err := RecordBackfillHistory(db, version, bf.Name(), dur, true, ""); err != nil {
		return err
	}
	logger.L.Info("tenant_backfill_success",
		slog.String("tenant", slug),
		slog.Duration("duration", dur),
	)
	return nil
}
