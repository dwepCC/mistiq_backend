package cmd

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/database/engine"
	"tukifac/pkg/logger"
	"tukifac/pkg/migrationlock"
	"tukifac/pkg/saas/docusage"
	"tukifac/pkg/tenantcache"
)

func printTenantProgress(slug string, err error) {
	fmt.Printf("Migrating tenant: %s\n", slug)
	if err != nil {
		fmt.Printf("✗ failed: %v\n", err)
	} else {
		fmt.Println("✓ completed")
	}
}

func migrateCentralDocumentQuota() error {
	return docusage.MigrateBillingCycleConstraints()
}

// RunMigrate solo BD central (deploy producción).
func RunMigrate() int {
	fmt.Println("Migrating central database...")
	if err := database.RunCentralMigration(); err != nil {
		fmt.Printf("✗ central failed: %v\n", err)
		return 1
	}
	if err := migrateCentralDocumentQuota(); err != nil {
		fmt.Printf("✗ document quota migrate: %v\n", err)
		return 1
	}
	fmt.Println("✓ central migrated")
	fmt.Println()
	fmt.Println("Fleet tenant migrations: use migrate-fleet (not bundled in deploy migrate).")
	return 0
}

// RunMigrateCentral solo BD central.
func RunMigrateCentral() int {
	fmt.Println("Migrating central database...")
	if err := database.RunCentralMigration(); err != nil {
		fmt.Printf("✗ central failed: %v\n", err)
		return 1
	}
	if err := migrateCentralDocumentQuota(); err != nil {
		fmt.Printf("✗ document quota migrate: %v\n", err)
		return 1
	}
	fmt.Println("✓ central migrated")
	return 0
}

// RunMigrateInitVersions bootstrap tenant_schema_versions V30 (idempotente).
func RunMigrateInitVersions() int {
	created, skipped, err := engine.InitTenantSchemaVersions()
	if err != nil {
		fmt.Printf("✗ init versions failed: %v\n", err)
		return 1
	}
	fmt.Printf("✓ tenant_schema_versions: created=%d skipped=%d (baseline V%d)\n",
		created, skipped, database.TenantSchemaBaselineVersion)
	return 0
}

// RunMigrateFleetCron ciclo completo del cron (bump + fleet + backfill) con lock global.
// Si otra instancia está activa, sale con código 0 sin mensajes (silencioso).
func RunMigrateFleetCron(args []string) int {
	fs := flag.NewFlagSet("migrate-fleet-cron", flag.ExitOnError)
	limit := fs.Int("limit", 100, "máximo tenants por ejecución")
	workers := fs.Int("workers", 4, "workers concurrentes")
	activeOnly := fs.Bool("active-only", true, "solo tenants activos")
	_ = fs.Parse(args)

	lease := fleetCronLockLease()
	release, ok := migrationlock.TryAcquireFleet(lease)
	if !ok {
		logger.L.Debug("fleet_cron_skipped", slog.String("reason", "lock_held"))
		return 0
	}
	defer release()

	if _, err := engine.BumpFleetTargetVersion(engine.CodeTargetVersion()); err != nil {
		fmt.Printf("⚠ bump-target: %v\n", err)
	}

	summary := engine.RunFleetMigrate(engine.FleetOptions{
		Limit:      *limit,
		Workers:    *workers,
		ActiveOnly: *activeOnly,
	})
	fleetRC := printFleetSummary(summary)

	backfill := engine.RunBackfillFleet(engine.BackfillOptions{
		Limit:      *limit,
		Workers:    *workers,
		Version:    0, // todos los backfills registrados (V31, V32, …); no usar CodeTargetVersion (schema)
		ActiveOnly: *activeOnly,
	})
	backfillRC := printTenantsSummary(backfill)

	if fleetRC != 0 || backfillRC != 0 {
		return 1
	}
	return 0
}

func fleetCronLockLease() time.Duration {
	if s := os.Getenv("MIGRATE_TIMEOUT_SEC"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	if s := os.Getenv("FLEET_LOCK_LEASE_SEC"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return time.Hour
}

// RunMigrateFleetResume cierra circuit breaker global del fleet.
func RunMigrateFleetResume() int {
	if err := database.ResetFleetCircuitBreaker(); err != nil {
		fmt.Printf("✗ fleet resume failed: %v\n", err)
		return 1
	}
	fmt.Println("✓ fleet circuit breaker cerrado")
	return 0
}

// RunMigrateFleet migración incremental fleet (workers concurrentes).
func RunMigrateFleet(args []string) int {
	fs := flag.NewFlagSet("migrate-fleet", flag.ExitOnError)
	limit := fs.Int("limit", 100, "máximo tenants por ejecución")
	workers := fs.Int("workers", 4, "workers concurrentes")
	activeOnly := fs.Bool("active-only", true, "solo tenants activos")
	_ = fs.Parse(args)

	summary := engine.RunFleetMigrate(engine.FleetOptions{
		Limit:      *limit,
		Workers:    *workers,
		ActiveOnly: *activeOnly,
	})
	return printFleetSummary(summary)
}

// RunMigrateBackfillFleet backfills run-once.
func RunMigrateBackfillFleet(args []string) int {
	fs := flag.NewFlagSet("migrate-backfill-fleet", flag.ExitOnError)
	limit := fs.Int("limit", 100, "máximo tenants")
	workers := fs.Int("workers", 4, "workers")
	version := fs.Int("version", 31, "versión backfill")
	tenant := fs.String("tenant", "", "slug único")
	activeOnly := fs.Bool("active-only", true, "solo activos")
	_ = fs.Parse(args)

	summary := engine.RunBackfillFleet(engine.BackfillOptions{
		Limit:      *limit,
		Workers:    *workers,
		Version:    *version,
		TenantSlug: *tenant,
		ActiveOnly: *activeOnly,
	})
	return printTenantsSummary(summary)
}

func printFleetSummary(summary engine.FleetSummary) int {
	fmt.Println()
	fmt.Printf("Fleet completed: success=%d failed=%d\n", len(summary.Success), len(summary.Failed))
	if summary.CircuitTripped {
		fmt.Printf("⚠ circuit breaker OPEN: %s\n", summary.CircuitReason)
		return 1
	}
	if len(summary.Failed) == 0 {
		return 0
	}
	for _, f := range summary.Failed {
		fmt.Printf("  - %s: %v\n", f.Slug, f.Err)
	}
	return 1
}

// RunMigrateTenants LEGACY bootstrap AutoMigrate (emergencia).
func RunMigrateTenants() int {
	if config.AppConfig != nil && config.AppConfig.IsProd() {
		fmt.Fprintln(os.Stderr, "✗ migrate-tenants deshabilitado en producción; use migrate-fleet")
		return 1
	}
	fmt.Println("LEGACY: bootstrap AutoMigrate per tenant (use migrate-fleet in production)...")
	return printEngineTenantsSummary(engine.MigrateTenantsBatch(true, printTenantProgress))
}

// RunMigrateBackfillBranch alias backfill V31.
func RunMigrateBackfillBranch() int {
	return RunMigrateBackfillFleet(nil)
}

// RunMigrateTenant bootstrap un tenant.
func RunMigrateTenant(slug string) int {
	fmt.Printf("Bootstrap migrate tenant: %s\n", slug)
	if err := engine.MigrateTenantBySlug(slug); err != nil {
		fmt.Printf("✗ failed: %v\n", err)
		return 1
	}
	fmt.Println("✓ completed")
	return 0
}

// RunRepairTenantMigrations reconcilia drift y re-ejecuta migraciones faltantes.
func RunRepairTenantMigrations(args []string) int {
	fs := flag.NewFlagSet("repair-tenant-migrations", flag.ExitOnError)
	slug := fs.String("slug", "", "tenant específico por slug")
	limit := fs.Int("limit", 50, "máximo tenants por lote")
	dryRun := fs.Bool("dry-run", false, "solo reportar drift sin modificar")
	reconcileOnly := fs.Bool("reconcile-only", false, "reconciliar drift sin ejecutar migraciones")
	_ = fs.Parse(args)

	mode := "repair+migrate"
	if *dryRun {
		mode = "dry-run"
	} else if *reconcileOnly {
		mode = "reconcile-only"
	}
	fmt.Printf("repair-tenant-migrations mode=%s limit=%d slug=%q\n", mode, *limit, *slug)

	outcomes, err := engine.RepairTenantMigrations(engine.RepairOptions{
		Slug:          *slug,
		Limit:         *limit,
		DryRun:        *dryRun,
		ReconcileOnly: *reconcileOnly,
	})
	if err != nil {
		fmt.Printf("✗ repair failed: %v\n", err)
		return 1
	}
	drifted := 0
	migrated := 0
	for _, o := range outcomes {
		fmt.Println(" ", engine.FormatRepairOutcome(o))
		if o.DriftDetected {
			drifted++
		}
		if o.Migrated {
			migrated++
		}
	}
	fmt.Printf("✓ processed=%d drifted=%d migrated=%d\n", len(outcomes), drifted, migrated)
	return 0
}

// RunMigrateBumpTarget sube target_version al código desplegado.
func RunMigrateBumpTarget() int {
	n, err := engine.BumpFleetTargetVersion(engine.CodeTargetVersion())
	if err != nil {
		fmt.Printf("✗ bump target failed: %v\n", err)
		return 1
	}
	fmt.Printf("✓ target_version -> V%d (rows=%d)\n", engine.CodeTargetVersion(), n)
	return 0
}

func printTenantsSummary(summary database.MigrateSummary) int {
	if len(summary.Failed) > 0 && len(summary.Failed) == 1 && summary.Failed[0].Slug == "(list)" {
		return 1
	}
	fmt.Println()
	fmt.Println("Migration completed")
	fmt.Printf("Tenants migrated: %d\n", len(summary.Success))
	if len(summary.Failed) == 0 {
		return 0
	}
	fmt.Printf("FAILED: %d\n", len(summary.Failed))
	for _, f := range summary.Failed {
		fmt.Printf("  - %s\n", f.Slug)
	}
	return 1
}

func printEngineTenantsSummary(summary engine.MigrateSummary) int {
	converted := database.MigrateSummary{
		Success: summary.Success,
		Failed:  summary.Failed,
	}
	return printTenantsSummary(converted)
}

// InitDatabase conecta la BD central y el tenant DB manager.
func InitDatabase() error {
	engine.SyncSchemaTargetVersion()
	if err := database.ConnectCentral(); err != nil {
		return err
	}
	if err := database.EnsureCentralFleetSchema(); err != nil {
		return err
	}
	database.InitTenantDBManager(config.AppConfig)
	if config.AppConfig != nil {
		tenantcache.InitRedis(config.AppConfig)
	}
	return nil
}

// AutoMigrateDev ejecuta migraciones al arrancar solo en desarrollo (AUTO_MIGRATE_DEV=true).
// En producción nunca migra en startup; usar deploy migrate-central + cron migrate-fleet.
func AutoMigrateDev() error {
	if config.AppConfig != nil && config.AppConfig.IsProd() {
		return nil
	}
	if os.Getenv("AUTO_MIGRATE_DEV") != "true" && os.Getenv("AUTO_MIGRATE_DEV") != "1" {
		return nil
	}
	fmt.Println("[dev] AUTO_MIGRATE_DEV: running migrations...")
	if err := database.RunCentralMigration(); err != nil {
		return err
	}
	if err := migrateCentralDocumentQuota(); err != nil {
		return err
	}
	if _, _, err := engine.InitTenantSchemaVersions(); err != nil {
		return err
	}
	if _, err := engine.BumpFleetTargetVersion(engine.CodeTargetVersion()); err != nil {
		return err
	}
	summary := engine.RunFleetMigrate(engine.FleetOptions{Limit: 0, Workers: 2, ActiveOnly: false})
	if len(summary.Failed) > 0 {
		return fmt.Errorf("fleet migrations failed: %d", len(summary.Failed))
	}
	fmt.Printf("[dev] fleet migrated %d tenants\n", len(summary.Success))
	return nil
}
