package cron

import (
	"log/slog"
	"strconv"
	"time"

	"tukifac/internal/exchangerate"
	"tukifac/pkg/cronlock"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/saas"
)

const (
	exchangeRateCronHour   = 9
	exchangeRateCronMinute = 5
)

// StartExchangeRateScheduler precarga TC del día y reintenta cada hora si está pending.
func StartExchangeRateScheduler() {
	go func() {
		waitForCentralSchema()
		runExchangeRateSchedulerLoop()
	}()
}

func runExchangeRateSchedulerLoop() {
	logger.L.Info("cron_started",
		slog.String("job", "exchange_rate_scheduler"),
		slog.String("daily_prefetch", "09:05 America/Lima"),
		slog.String("hourly_retry", "pending today"),
	)

	runExchangeRateDailyPrefetch(true)
	runExchangeRateHourlyRetry()
	// Cada día debe tener su TC guardado: al iniciar se completan los últimos 45 días que falten.
	runExchangeRateGapFill(45)

	dailyTicker := time.NewTicker(30 * time.Second)
	hourlyTicker := time.NewTicker(1 * time.Hour)
	defer dailyTicker.Stop()
	defer hourlyTicker.Stop()

	var lastDailyDate, lastGapFillDate string

	for {
		select {
		case <-dailyTicker.C:
			now := saas.NowLima()
			today := now.Format("2006-01-02")
			if now.Hour() == exchangeRateCronHour && now.Minute() == exchangeRateCronMinute && lastDailyDate != today {
				lastDailyDate = today
				runExchangeRateDailyPrefetch(false)
			}
			// 00:10: el día nuevo ya tiene fila (propagada del último TC confirmado) antes de que llegue la primera operación;
			// a las 09:05 el prefetch la confirma con el TC publicado.
			if now.Hour() == 0 && now.Minute() == 10 && lastGapFillDate != today {
				lastGapFillDate = today
				runExchangeRateGapFill(5)
			}
		case <-hourlyTicker.C:
			runExchangeRateHourlyRetry()
		}
	}
}

func runExchangeRateDailyPrefetch(initial bool) {
	if !database.IsCentralSchemaReady() {
		return
	}
	today := saas.NowLima().Format("2006-01-02")
	release, acquired := cronlock.TryAcquireDaily("exchange_rate:prefetch", today, 20*time.Minute)
	if !acquired {
		return
	}
	defer release()

	svc := exchangerate.DefaultService()
	res, err := svc.ForceRefresh(today)
	logger.L.Info("exchange_rate_daily_prefetch",
		slog.Bool("initial", initial),
		slog.String("fecha", today),
		slog.Bool("success", res != nil && res.Success),
		slog.String("status", safeStatus(res)),
		slog.Any("error", err),
	)
}

// runExchangeRateGapFill completa los días sin TC guardado de los últimos daysBack días (idempotente; un solo nodo a la vez).
func runExchangeRateGapFill(daysBack int) {
	if !database.IsCentralSchemaReady() {
		return
	}
	day := saas.NowLima().Format("2006-01-02")
	release, acquired := cronlock.TryAcquireDaily("exchange_rate:gapfill:"+strconv.Itoa(daysBack), day, 30*time.Minute)
	if !acquired {
		return
	}
	defer release()
	filled := exchangerate.DefaultService().EnsureRecentDays(daysBack)
	logger.L.Info("exchange_rate_gap_fill", slog.Int("days_back", daysBack), slog.Int("filled", filled))
}

func runExchangeRateHourlyRetry() {
	if !database.IsCentralSchemaReady() {
		return
	}
	now := saas.NowLima()
	hourKey := now.Format("2006-01-02-15")
	release, acquired := cronlock.TryAcquire("exchange_rate:hourly:"+hourKey, 50*time.Minute)
	if !acquired {
		return
	}
	defer release()

	today := now.Format("2006-01-02")
	svc := exchangerate.DefaultService()
	res, err := svc.GetExchangeRate(today)
	logger.L.Debug("exchange_rate_hourly_retry",
		slog.String("fecha", today),
		slog.Bool("success", res != nil && res.Success),
		slog.String("status", safeStatus(res)),
		slog.Any("error", err),
	)
}

func safeStatus(res *exchangerate.QueryResult) string {
	if res == nil {
		return ""
	}
	return res.Status
}
