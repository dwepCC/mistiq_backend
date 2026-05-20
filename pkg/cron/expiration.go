package cron

import (
	"log/slog"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/logger"
)

const schemaPollInterval = 10 * time.Second

// StartExpirationChecker inicia el cron solo cuando el esquema central está listo.
// Si aún no hay tablas/columnas (p. ej. antes de migrate-central), espera en background.
func StartExpirationChecker() {
	go func() {
		waitForCentralSchema()
		runExpirationCheckerLoop()
	}()
}

func waitForCentralSchema() {
	if database.IsCentralSchemaReady() {
		return
	}
	logger.L.Warn("cron_waiting_for_central_schema",
		slog.String("job", "expiration_checker"),
		slog.Duration("poll", schemaPollInterval),
	)
	ticker := time.NewTicker(schemaPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		if database.IsCentralSchemaReady() {
			logger.L.Info("cron_central_schema_ready", slog.String("job", "expiration_checker"))
			return
		}
	}
}

func runExpirationCheckerLoop() {
	logger.L.Info("cron_started", slog.String("job", "expiration_checker"), slog.String("interval", "24h"))
	checkExpirations()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		checkExpirations()
	}
}

// checkExpirations marca suscripciones vencidas como "expired" (solo informativo).
// IMPORTANTE: NO modifica Tenant.Status. La suspensión del tenant es SIEMPRE manual
// desde el panel central. El admin verá las suscripciones vencidas y decidirá.
func checkExpirations() {
	if !database.IsCentralSchemaReady() {
		logger.L.Warn("cron_skipped_schema_not_ready", slog.String("job", "expiration_checker"))
		return
	}

	now := time.Now()
	var expired []database.SaasSubscription
	if err := database.CentralDB.
		Where("status = 'active' AND end_date < ?", now).
		Find(&expired).Error; err != nil {
		logger.L.Error("cron_expiration_query_failed",
			slog.String("job", "expiration_checker"),
			slog.Any("error", err),
		)
		return
	}

	if len(expired) == 0 {
		return
	}

	for _, sub := range expired {
		if err := database.CentralDB.Model(&sub).Update("status", "expired").Error; err != nil {
			logger.L.Error("cron_expiration_update_failed",
				slog.Uint64("subscription_id", uint64(sub.ID)),
				slog.Any("error", err),
			)
			continue
		}
		logger.L.Info("subscription_expired",
			slog.Uint64("subscription_id", uint64(sub.ID)),
			slog.Uint64("tenant_id", uint64(sub.TenantID)),
		)
	}
	logger.L.Info("cron_expiration_run_complete",
		slog.Int("expired_count", len(expired)),
		slog.String("note", "tenant_status_not_modified"),
	)
}
