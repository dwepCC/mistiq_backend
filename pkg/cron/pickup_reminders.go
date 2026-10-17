package cron

import (
	"context"
	"log/slog"
	"time"

	ecomsvc "tukifac/internal/ecommerce/service"
	"tukifac/pkg/cronlock"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/notificationevents"
)

const pickupReminderInterval = 6 * time.Hour

// StartPickupReminderWorker avisa de los paquetes que están por vencer (o vencieron) su plazo de recojo en agencia.
func StartPickupReminderWorker() {
	go func() {
		waitForCentralSchema()
		logger.L.Info("cron_started", slog.String("job", "pickup_reminders"), slog.Duration("interval", pickupReminderInterval))
		runPickupRemindersOnce()
		ticker := time.NewTicker(pickupReminderInterval)
		defer ticker.Stop()
		for range ticker.C {
			runPickupRemindersOnce()
		}
	}()
}

func runPickupRemindersOnce() {
	if !database.IsCentralSchemaReady() {
		return
	}
	release, acquired := cronlock.TryAcquire("ecommerce:pickup_reminders", pickupReminderInterval-10*time.Minute)
	if !acquired {
		return
	}
	defer release()

	tenants, err := database.ListTenantsForMigration(true)
	if err != nil {
		logger.L.Warn("pickup_reminders_list_tenants_failed", slog.Any("error", err))
		return
	}
	for _, t := range tenants {
		db, err := database.GetTenantDB(t.DBName)
		if err != nil {
			continue
		}
		n, err := ecomsvc.NewEcommerceService(db).NotifyPickupDeadlines(time.Now())
		database.ReleaseTenantDB(t.DBName)
		if err != nil {
			// Un negocio sin las tablas de envíos todavía (migración pendiente) no debe frenar a los demás.
			logger.L.Debug("pickup_reminders_tenant_failed", slog.String("tenant", t.Slug), slog.Any("error", err))
			continue
		}
		if n > 0 {
			logger.L.Info("pickup_reminders_created", slog.String("tenant", t.Slug), slog.Int("count", n))
			notificationevents.PublishChanged(context.Background(), t.ID)
		}
	}
}
