package cron

import (
	"fmt"
	"log/slog"
	"time"

	"tukifac/pkg/cronlock"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/saas"
)

const schemaPollInterval = 10 * time.Second

func waitForCentralSchema() {
	if database.IsCentralSchemaReady() {
		return
	}
	logger.L.Warn("cron_waiting_for_central_schema",
		slog.Duration("poll", schemaPollInterval),
	)
	ticker := time.NewTicker(schemaPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		if database.IsCentralSchemaReady() {
			logger.L.Info("cron_central_schema_ready")
			return
		}
	}
}

// StartSaasScheduler: evaluación diaria 00:05 America/Lima + jobs horarios auxiliares.
func StartSaasScheduler() {
	go func() {
		waitForCentralSchema()
		runSaasSchedulerLoop()
	}()
}

func runSaasSchedulerLoop() {
	logger.L.Info("cron_started",
		slog.String("job", "saas_scheduler"),
		slog.String("daily_eval", "00:05 America/Lima"),
		slog.String("hourly", "reminders"),
	)

	runLimaDailyIfDue(true)
	runHourlySaasJobs()

	limaTicker := time.NewTicker(30 * time.Second)
	hourlyTicker := time.NewTicker(1 * time.Hour)
	defer limaTicker.Stop()
	defer hourlyTicker.Stop()

	var lastDailyDate string

	for {
		select {
		case <-limaTicker.C:
			now := saas.NowLima()
			today := now.Format("2006-01-02")
			cfg, _ := saas.LoadSettings()
			h, m := cfg.CronEvalHour, cfg.CronEvalMinute
			if m < 0 || m > 59 {
				m = 5
			}
			if h < 0 || h > 23 {
				h = 0
			}
			runAt := now.Hour() == h && now.Minute() == m
			if runAt && lastDailyDate != today {
				lastDailyDate = today
				runLimaDailyIfDue(false)
			}
		case <-hourlyTicker.C:
			runHourlySaasJobs()
		}
	}
}

func runLimaDailyIfDue(initial bool) {
	if !database.IsCentralSchemaReady() {
		return
	}
	now := saas.NowLima()
	today := now.Format("2006-01-02")
	release, acquired := cronlock.TryAcquireDaily("saas:lima_daily", today, 45*time.Minute)
	if !acquired {
		logger.L.Debug("saas_lima_daily_skipped_lock", slog.Bool("initial", initial), slog.String("date", today))
		return
	}
	defer release()

	su, s, oc := saas.RunLimaDailyEvaluation()
	logger.L.Info("saas_lima_daily_run",
		slog.Bool("initial", initial),
		slog.Int("status_updates", su),
		slog.Int("suspended", s),
		slog.Int("overdue_cycles", oc),
	)
}

func runHourlySaasJobs() {
	if !database.IsCentralSchemaReady() {
		return
	}
	now := saas.NowLima()
	hourKey := fmt.Sprintf("%s-%02d", now.Format("2006-01-02"), now.Hour())
	release, acquired := cronlock.TryAcquire("saas:hourly:"+hourKey, 55*time.Minute)
	if !acquired {
		return
	}
	defer release()

	r, n := saas.RunHourlyJobs()
	logger.L.Debug("saas_hourly_jobs", slog.Int("reminders", r), slog.Int("notifications", n))
}
