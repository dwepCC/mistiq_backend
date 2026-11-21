package cron

import (
	"log/slog"
	"time"

	"tukifac/internal/fiscal/summary"
	"tukifac/pkg/cronlock"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/saas"
)

const (
	fiscalSnapshotInterval = 15 * time.Minute
	fiscalSnapshotWorkers  = 4
	// Pasada profunda (historial completo + abiertos antiguos): una vez al día a partir de las 00:30 Lima.
	fiscalSnapshotDeepHour   = 0
	fiscalSnapshotDeepMinute = 30
)

// StartFiscalTenantSnapshotWorker mantiene el resumen fiscal por tenant en la BD central
// (tenant_fiscal_daily / tenant_fiscal_health). Solo LEE las bases de los tenants.
// Ver docs/PLAN-RESUMEN-FISCAL-POR-TENANT.md.
func StartFiscalTenantSnapshotWorker() {
	go func() {
		waitForCentralSchema()
		runFiscalSnapshotLoop()
	}()
}

func runFiscalSnapshotLoop() {
	logger.L.Info("cron_started",
		slog.String("job", "fiscal_tenant_snapshot"),
		slog.Duration("interval", fiscalSnapshotInterval),
		slog.Int("workers", fiscalSnapshotWorkers),
	)

	runFiscalSnapshotOnce()

	ticker := time.NewTicker(fiscalSnapshotInterval)
	defer ticker.Stop()
	for range ticker.C {
		runFiscalSnapshotOnce()
	}
}

func runFiscalSnapshotOnce() {
	if !database.IsCentralSchemaReady() || !fiscalSnapshotTablesReady() {
		return
	}
	// TTL algo menor que el intervalo: si una instancia muere con el candado tomado, el siguiente
	// ciclo puede continuar.
	release, acquired := cronlock.TryAcquire("fiscal:tenant_snapshot", 14*time.Minute)
	if !acquired {
		logger.L.Debug("fiscal_snapshot_skipped_lock")
		return
	}
	defer release()

	deep := fiscalSnapshotNeedsDeep()
	if _, err := summary.NewScanner().ScanAll(deep, fiscalSnapshotWorkers); err != nil {
		logger.L.Warn("fiscal_snapshot_failed", slog.Any("error", err))
	}
}

// Las tablas las crea migrate-central en el deploy; si el job arranca antes, no hace nada.
func fiscalSnapshotTablesReady() bool {
	m := database.CentralDB.Migrator()
	return m.HasTable(&database.TenantFiscalDaily{}) && m.HasTable(&database.TenantFiscalHealth{})
}

// La primera vez (tabla de salud vacía) y cada noche pasada la hora configurada se hace la pasada
// profunda; el resto de los ciclos solo recalcula la ventana reciente.
func fiscalSnapshotNeedsDeep() bool {
	var n int64
	if err := database.CentralDB.Model(&database.TenantFiscalHealth{}).Count(&n).Error; err == nil && n == 0 {
		return true
	}
	now := saas.NowLima()
	if now.Hour() != fiscalSnapshotDeepHour || now.Minute() < fiscalSnapshotDeepMinute {
		return false
	}
	_, ok := cronlock.TryAcquireDaily("fiscal:tenant_snapshot_deep", now.Format("2006-01-02"), 20*time.Hour)
	return ok
}
