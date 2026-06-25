package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantmigrations"
	"tukifac/pkg/logger"
	"tukifac/pkg/metrics"

	"gorm.io/gorm"
)

// RunTenantSchemaMigrations ejecuta migraciones registradas con versión en (fromV, toV].
func RunTenantSchemaMigrations(db *gorm.DB, slug string, fromV, toV int) error {
	if toV <= fromV {
		return nil
	}
	if err := ensureTenantMigrationHistoryTable(db); err != nil {
		return err
	}

	migrations := tenantmigrations.MigrationsUpTo(fromV, toV)
	if len(migrations) == 0 {
		return nil
	}

	for _, mig := range migrations {
		v := mig.Version()
		if applied, _ := isHistoryApplied(db, v, database.MigrationHistoryTypeSchema); applied {
			logger.L.Info("tenant_migration_skip",
				slog.String("tenant", slug),
				slog.String("tenant_slug", slug),
				slog.Int("version", v),
				slog.String("reason", "already_applied"),
			)
			continue
		}

		logger.L.Info("tenant_migration_start",
			slog.String("tenant", slug),
			slog.Int("version", v),
			slog.String("name", mig.Name()),
		)
		start := time.Now()
		err := mig.Up(db)
		dur := time.Since(start)
		checksum := migrationChecksum(v, mig.Name())
		if err != nil {
			_ = recordHistory(db, v, mig.Name(), database.MigrationHistoryTypeSchema, dur, false, err.Error(), checksum)
			metrics.MigrationFailedTotal.Add(1)
			logger.L.Error("tenant_migration_failed",
				slog.String("tenant", slug),
				slog.Int("version", v),
				slog.Int64("duration_ms", dur.Milliseconds()),
				slog.Any("error", err),
			)
			return fmt.Errorf("V%d %s: %w", v, mig.Name(), err)
		}
		_ = recordHistory(db, v, mig.Name(), database.MigrationHistoryTypeSchema, dur, true, "", checksum)
		metrics.MigrationDurationMsTotal.Add(dur.Milliseconds())
		metrics.MigrationSuccessTotal.Add(1)
		logger.L.Info("tenant_migration_success",
			slog.String("tenant", slug),
			slog.Int("version", v),
			slog.Int64("duration_ms", dur.Milliseconds()),
		)
	}

	if err := ValidateSchemaAtVersion(db, toV); err != nil {
		return err
	}
	return nil
}

func migrationChecksum(version int, name string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("schema:%d:%s", version, name)))
	return hex.EncodeToString(sum[:])
}

func ensureTenantMigrationHistoryTable(db *gorm.DB) error {
	return db.AutoMigrate(&database.TenantMigrationHistory{})
}

func isHistoryApplied(db *gorm.DB, version int, typ string) (bool, error) {
	if !db.Migrator().HasTable(&database.TenantMigrationHistory{}) {
		return false, nil
	}
	var n int64
	err := db.Model(&database.TenantMigrationHistory{}).
		Where("version = ? AND type = ? AND success = ?", version, typ, true).
		Count(&n).Error
	return n > 0, err
}

func recordHistory(db *gorm.DB, version int, name, typ string, dur time.Duration, success bool, errMsg, checksum string) error {
	if !db.Migrator().HasTable(&database.TenantMigrationHistory{}) {
		if err := ensureTenantMigrationHistoryTable(db); err != nil {
			return err
		}
	}
	row := database.TenantMigrationHistory{
		Version:    version,
		Name:       name,
		Type:       typ,
		AppliedAt:  time.Now(),
		DurationMs: dur.Milliseconds(),
		Success:    success,
		Checksum:   &checksum,
	}
	if errMsg != "" {
		row.Error = &errMsg
	}
	return db.Create(&row).Error
}

// IsBackfillApplied consulta history run-once para backfill.
func IsBackfillApplied(db *gorm.DB, version int) (bool, error) {
	return isHistoryApplied(db, version, database.MigrationHistoryTypeBackfill)
}

// RecordBackfillHistory registra backfill en tenant_migration_history.
func RecordBackfillHistory(db *gorm.DB, version int, name string, dur time.Duration, success bool, errMsg string) error {
	return recordHistory(db, version, name, database.MigrationHistoryTypeBackfill, dur, success, errMsg, migrationChecksum(version, name))
}
