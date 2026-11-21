package tenantcache

import (
	"context"
	"log/slog"
	"time"

	"tukifac/config"
	"tukifac/pkg/logger"
	"tukifac/pkg/metrics"

	"github.com/redis/go-redis/v9"
)

var globalRDB *redis.Client

// InitRedis conecta a Redis según config.Redis. Si falla o está deshabilitado, opera sin Redis (fallback).
func InitRedis(cfg *config.Config) *redis.Client {
	rs := cfg.Redis

	logger.L.Info("redis_initializing",
		slog.String("redis_addr", rs.RedisSafeAddr()),
		slog.Bool("redis_enabled", rs.Enabled),
		slog.Int("redis_db", rs.DB),
		slog.Int("redis_pool_size", rs.PoolSize),
		slog.Int("redis_min_idle_conns", rs.MinIdleConns),
		slog.Int("redis_max_retries", rs.MaxRetries),
	)

	if !rs.Enabled {
		logger.L.Info("redis_disabled",
			slog.String("reason", "REDIS_DISABLED or REDIS_URL=none"),
			slog.Bool("fallback_mode_enabled", true),
			slog.Bool("billing_async", false),
			slog.Bool("tenant_cache_enabled", false),
		)
		return nil
	}

	if rs.URL == "" {
		logger.L.Error("redis_connection_failed",
			slog.String("redis_addr", rs.RedisSafeAddr()),
			slog.Bool("fallback_mode_enabled", true),
			slog.Bool("billing_async", false),
			slog.String("error", "empty REDIS_URL after resolve"),
		)
		return nil
	}

	opt, err := redis.ParseURL(rs.URL)
	if err != nil {
		logger.L.Error("redis_connection_failed",
			slog.String("redis_addr", rs.RedisSafeAddr()),
			slog.Bool("fallback_mode_enabled", true),
			slog.Bool("billing_async", false),
			slog.String("error", "invalid redis url: "+err.Error()),
		)
		return nil
	}

	opt.PoolSize = rs.PoolSize
	if opt.PoolSize < 1 {
		opt.PoolSize = 32
	}
	opt.MinIdleConns = rs.MinIdleConns
	if opt.MinIdleConns < 1 {
		opt.MinIdleConns = 2
	}
	opt.MaxRetries = rs.MaxRetries
	if opt.MaxRetries < 0 {
		opt.MaxRetries = 3
	}
	opt.ReadTimeout = 3 * time.Second
	opt.WriteTimeout = 3 * time.Second

	rdb := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	pingErr := rdb.Ping(ctx).Err()
	pingMs := time.Since(start).Milliseconds()

	if pingErr != nil {
		logger.L.Error("redis_connection_failed",
			slog.String("redis_addr", opt.Addr),
			slog.Bool("fallback_mode_enabled", true),
			slog.Bool("billing_async", false),
			slog.Bool("tenant_cache_enabled", false),
			slog.Any("error", pingErr),
		)
		_ = rdb.Close()
		return nil
	}

	globalRDB = rdb
	logger.L.Info("redis_connected",
		slog.String("redis_addr", opt.Addr),
		slog.Bool("redis_connected", true),
		slog.Int64("redis_ping_ms", pingMs),
		slog.Int("redis_pool_size", opt.PoolSize),
		slog.Int("redis_db", opt.DB),
		slog.Bool("tenant_cache_enabled", true),
	)
	return rdb
}

// RDB retorna el cliente global (puede ser nil).
func RDB() *redis.Client {
	return globalRDB
}

// Connected indica si hay cliente Redis activo.
func Connected() bool {
	return globalRDB != nil
}

// Close cierra Redis en shutdown.
func Close() error {
	if globalRDB == nil {
		return nil
	}
	err := globalRDB.Close()
	globalRDB = nil
	return err
}

func redisOK() {
	metrics.RedisOpsOK.Add(1)
}

func redisFail() {
	metrics.RedisOpsFail.Add(1)
}

// SetClientForTests instala rdb como cliente global y devuelve la función que restaura el anterior. Solo para pruebas
// (p. ej. con miniredis); producción usa InitRedis.
func SetClientForTests(rdb *redis.Client) (restore func()) {
	prev := globalRDB
	globalRDB = rdb
	return func() { globalRDB = prev }
}
