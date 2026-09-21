package notificationevents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tukifac/pkg/logger"
)

func tenantChannel(tenantID uint) string {
	return fmt.Sprintf("tukifac:tenant:%d:notification_updates", tenantID)
}

// PublishChanged publica en Redis Pub/Sub (fire-and-forget; nunca bloquea al caller HTTP).
func PublishChanged(ctx context.Context, tenantID uint) {
	if globalHub == nil || globalHub.rdb == nil || tenantID == 0 {
		return
	}
	data, err := json.Marshal(NewChanged(tenantID))
	if err != nil {
		return
	}
	if err := globalHub.rdb.Publish(ctx, tenantChannel(tenantID), data).Err(); err != nil {
		logger.L.Warn("notificationevents_publish_failed",
			slog.Uint64("tenant_id", uint64(tenantID)),
			slog.Any("error", err),
		)
	}
}
