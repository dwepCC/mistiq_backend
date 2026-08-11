package docusage

import (
	"log/slog"

	"tukifac/pkg/database"
	"tukifac/pkg/logger"
)

// ExpirePackagesForEndedCycles marca paquetes vencidos al cierre de ciclo (00:05 Lima).
func ExpirePackagesForEndedCycles() int {
	if database.CentralDB == nil {
		return 0
	}
	now := nowLima()
	today := calendarDateLima(now)

	res := database.CentralDB.Model(&database.SaasTenantDocumentPackage{}).
		Where("status = ? AND expires_at < ?", database.SaasDocPkgApproved, today).
		Updates(map[string]interface{}{
			"status":              database.SaasDocPkgExpired,
			"remaining_documents": 0,
		})
	if res.RowsAffected > 0 {
		logger.L.Info("saas_document_packages_expired",
			slog.Int64("count", res.RowsAffected),
			slog.String("timezone", limaTZ),
		)
	}
	return int(res.RowsAffected)
}
