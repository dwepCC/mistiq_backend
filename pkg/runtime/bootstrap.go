package runtime

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tukifac/config"
	billworker "tukifac/internal/billing/worker"
	fiscalworker "tukifac/internal/fiscal/worker"
	"tukifac/pkg/billingevents"
	"tukifac/pkg/billingqueue"
	"tukifac/pkg/database"
	"tukifac/pkg/database/engine"
	"tukifac/pkg/fiscaladmin"
	"tukifac/pkg/fiscalclient"
	"tukifac/pkg/fiscalqueue"
	"tukifac/pkg/logger"
	"tukifac/pkg/tenantcache"
)

// Init infraestructura de producción (Redis, tenant cache, DB manager, billing workers).
func Init(cfg *config.Config) error {
	engine.SyncSchemaTargetVersion()
	if err := database.ConnectCentral(); err != nil {
		return err
	}
	if err := database.EnsureCentralFleetSchema(); err != nil {
		return err
	}
	database.InitTenantDBManager(cfg)

	rdb := tenantcache.InitRedis(cfg)
	tenantcache.Init(cfg, rdb)
	billingevents.Init(rdb)

	if cfg.FacturadorBaseURL != "" && cfg.FacturadorToken != "" {
		fiscalclient.Init(cfg.FacturadorBaseURL, cfg.FacturadorToken)
		fiscaladmin.Init(cfg.FacturadorBaseURL, cfg.FacturadorToken)
	}

	billingqueue.Start(cfg, rdb, billworker.ProcessJob)
	fiscalqueue.Start(cfg, rdb, fiscalworker.ProcessJob)

	billingAsync := billingqueue.Enabled()
	tenantCache := tenantcache.Connected()

	logger.L.Info("runtime_initialized",
		slog.Bool("redis", tenantCache),
		slog.String("redis_addr", cfg.Redis.RedisSafeAddr()),
		slog.Bool("redis_connected", tenantCache),
		slog.Bool("tenant_cache_enabled", tenantCache),
		slog.Bool("billing_async", billingAsync),
		slog.Bool("fiscal_enabled", fiscalclient.Enabled()),
		slog.Bool("fallback_mode_enabled", !tenantCache),
		slog.Int("tenant_pool_max", cfg.TenantPoolMaxActive),
	)

	if cfg.IsProd() && cfg.Redis.Enabled && !tenantCache {
		logger.L.Warn("redis_expected_in_production",
			slog.String("hint", "set REDIS_URL=redis://tukifac-redis:6379/0 or REDIS_ADDR=tukifac-redis:6379 in .env; ensure backend and redis share Docker network"),
		)
	}

	return nil
}

// Shutdown graceful de pools y Redis.
func Shutdown() {
	billingqueue.Stop()
	fiscalqueue.Stop()
	billingevents.Shutdown()
	database.ShutdownTenantDBManager()
	_ = tenantcache.Close()
}

// ListenShutdown registra SIGINT/SIGTERM para cerrar pools.
func ListenShutdown() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		logger.L.Info("shutdown_signal_received")
		Shutdown()
		os.Exit(0)
	}()
}
