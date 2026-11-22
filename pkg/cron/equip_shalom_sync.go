package cron

import (
	"context"
	"log/slog"
	"time"

	"tukifac/internal/equipos/service"
	"tukifac/pkg/cronlock"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
)

// equipShalomSyncCheck cada cuánto se evalúa si toca sincronizar. La sincronización en sí solo corre si la
// última tiene más de 24 h (SyncAgencies lo decide) y solo si la integración está activa: la API de Shalom
// no se consulta de más.
const equipShalomSyncCheck = time.Hour

// StartEquipShalomAgencySync mantiene el catálogo de agencias de Shalom al día en la BD central.
func StartEquipShalomAgencySync() {
	go func() {
		waitForCentralSchema()
		runEquipShalomSyncOnce()
		ticker := time.NewTicker(equipShalomSyncCheck)
		defer ticker.Stop()
		for range ticker.C {
			runEquipShalomSyncOnce()
		}
	}()
}

func runEquipShalomSyncOnce() {
	if !database.IsCentralSchemaReady() {
		return
	}
	release, acquired := cronlock.TryAcquire("equipos:shalom_agencies", 10*time.Minute)
	if !acquired {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := service.New(database.CentralDB).SyncAgencies(ctx, false)
	if err != nil {
		if !service.IsValidation(err) { // integración inactiva o sin API key: no es un fallo
			logger.L.Warn("equip_shalom_agency_sync_failed", slog.String("error", err.Error()))
		}
		return
	}
	if !res.Skipped {
		logger.L.Info("equip_shalom_agency_sync_done", slog.Int("total", res.Total), slog.Int("created", res.Created), slog.Int("updated", res.Updated))
	}
}
