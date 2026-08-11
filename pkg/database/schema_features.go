package database

import "gorm.io/gorm"

// SchemaAtLeast indica si el tenant tiene aplicada la migración de esquema >= minVersion.
// Compatible con fleet heterogéneo: history + fallback HasColumn.
func SchemaAtLeast(db *gorm.DB, minVersion int) bool {
	if db == nil {
		return false
	}
	if minVersion <= TenantSchemaBaselineVersion {
		return true
	}
	if db.Migrator().HasTable(&TenantMigrationHistory{}) {
		var n int64
		_ = db.Model(&TenantMigrationHistory{}).
			Where("version >= ? AND type = ? AND success = ?", minVersion, MigrationHistoryTypeSchema, true).
			Count(&n).Error
		if n > 0 {
			return true
		}
	}
	return schemaAtLeastFallback(db, minVersion)
}

func schemaAtLeastFallback(db *gorm.DB, minVersion int) bool {
	switch minVersion {
	case 31:
		return TenantBranchMultiSchemaReady(db)
	case 32:
		return db.Migrator().HasColumn("tenant_table_sessions", "order_type")
	case 35:
		return db.Migrator().HasTable("tenant_restaurant_staff")
	default:
		return false
	}
}

// TenantFeatureFlags capacidades derivadas del esquema (para API / frontends).
func TenantFeatureFlags(db *gorm.DB) map[string]bool {
	flags := map[string]bool{
		"multi_branch":        SchemaAtLeast(db, 31),
		"restaurant_orders":   SchemaAtLeast(db, 32),
		"restaurant_staff_v2": SchemaAtLeast(db, 35),
		"restaurant_recipes":  false,
		"advanced_inventory":  false,
	}
	if SchemaAtLeast(db, 35) {
		var cfg TenantRestaurantSetting
		if db.First(&cfg).Error == nil {
			flags["restaurant_staff_v2_enabled"] = cfg.StaffV2Enabled
		}
	}
	return flags
}

// CurrentSchemaVersion estima versión actual del tenant (history > fallback > baseline).
func CurrentSchemaVersion(db *gorm.DB) int {
	if db == nil {
		return TenantSchemaBaselineVersion
	}
	if db.Migrator().HasTable(&TenantMigrationHistory{}) {
		var maxVer *int
		_ = db.Model(&TenantMigrationHistory{}).
			Where("type = ? AND success = ?", MigrationHistoryTypeSchema, true).
			Select("COALESCE(MAX(version), 0)").Scan(&maxVer).Error
		if maxVer != nil && *maxVer > 0 {
			return *maxVer
		}
	}
	if db.Migrator().HasColumn("tenant_table_sessions", "order_type") {
		return 32
	}
	if TenantBranchMultiSchemaReady(db) {
		return 31
	}
	return TenantSchemaBaselineVersion
}
