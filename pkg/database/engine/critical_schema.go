package engine

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// criticalSchemaCheck objeto físico esperado tras una migración versionada.
type criticalSchemaCheck struct {
	version int
	table   string
	label   string
}

// criticalSchemaChecks tablas validadas en detección de drift físico.
var criticalSchemaChecks = []criticalSchemaCheck{
	{31, "tenant_branches", "tabla tenant_branches"},
	{35, "tenant_restaurant_staff", "tabla tenant_restaurant_staff"},
	{59, "tenant_user_branches", "tabla tenant_user_branches"},
}

// detectPhysicalDriftMinVersion primera versión con historial success pero objeto faltante.
func detectPhysicalDriftMinVersion(db *gorm.DB) (int, bool) {
	if db == nil {
		return 0, false
	}
	mig := db.Migrator()
	minV := 0
	for _, c := range criticalSchemaChecks {
		if mig.HasTable(c.table) {
			continue
		}
		applied, _ := isHistoryApplied(db, c.version, database.MigrationHistoryTypeSchema)
		if !applied {
			continue
		}
		if minV == 0 || c.version < minV {
			minV = c.version
		}
	}
	return minV, minV > 0
}
