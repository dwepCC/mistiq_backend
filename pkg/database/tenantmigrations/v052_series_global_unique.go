package tenantmigrations

import (
	"fmt"
	"log"

	"gorm.io/gorm"
)

// V052SeriesGlobalUnique: serie única a nivel tenant (SUNAT) + retira índice incorrecto si existía.
type V052SeriesGlobalUnique struct{}

func (V052SeriesGlobalUnique) Version() int { return 52 }
func (V052SeriesGlobalUnique) Name() string { return "series_global_unique" }

const (
	idxDocumentSeriesCode     = "uk_tenant_document_series_series"
	idxSaleBranchNumberLegacy = "uk_tenant_sales_branch_number"
)

func (V052SeriesGlobalUnique) Up(db *gorm.DB) error {
	// Retirar índice incorrecto de despliegues tempranos de v050 (no cumple regla SUNAT).
	if db.Migrator().HasTable("tenant_sales") && migrationHasIndex(db, "tenant_sales", idxSaleBranchNumberLegacy) {
		if err := db.Exec(fmt.Sprintf(`DROP INDEX %s ON tenant_sales`, idxSaleBranchNumberLegacy)).Error; err != nil {
			log.Printf("[v052] tenant: no se pudo eliminar %s: %v", idxSaleBranchNumberLegacy, err)
		} else {
			log.Printf("[v052] tenant: eliminado índice legacy %s", idxSaleBranchNumberLegacy)
		}
	}

	if !db.Migrator().HasTable("tenant_document_series") {
		return nil
	}

	dupSeries, err := countDuplicateGroups(db, `
		SELECT COUNT(*) FROM (
			SELECT UPPER(TRIM(series)) AS series_key
			FROM tenant_document_series
			GROUP BY series_key
			HAVING COUNT(*) > 1
		) dup`)
	if err != nil {
		return fmt.Errorf("auditoría series duplicadas: %w", err)
	}
	if dupSeries > 0 {
		log.Printf("[v052] tenant: omitiendo UNIQUE(series) — códigos duplicados=%d (scripts/audit_document_series_global.sql)", dupSeries)
		logDocumentSeriesDuplicateSamples(db)
		return nil
	}

	if !migrationHasIndex(db, "tenant_document_series", idxDocumentSeriesCode) {
		if err := db.Exec(fmt.Sprintf(
			`CREATE UNIQUE INDEX %s ON tenant_document_series (series)`,
			idxDocumentSeriesCode,
		)).Error; err != nil {
			return fmt.Errorf("crear %s: %w", idxDocumentSeriesCode, err)
		}
	}

	return nil
}

func logDocumentSeriesDuplicateSamples(db *gorm.DB) {
	var rows []struct {
		Series   string
		Cantidad int64
		IDs      string
		Branches string
	}
	_ = db.Raw(`
		SELECT UPPER(TRIM(series)) AS series, COUNT(*) AS cantidad,
			GROUP_CONCAT(id ORDER BY id) AS ids,
			GROUP_CONCAT(DISTINCT branch_id ORDER BY branch_id) AS branches
		FROM tenant_document_series
		GROUP BY UPPER(TRIM(series))
		HAVING COUNT(*) > 1
		LIMIT 10
	`).Scan(&rows).Error
	for _, r := range rows {
		log.Printf("[v052] dup series=%s count=%d ids=%s branches=%s", r.Series, r.Cantidad, r.IDs, r.Branches)
	}
}

func migrationHasIndex(db *gorm.DB, table, indexName string) bool {
	var n int64
	_ = db.Raw(`
		SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?
	`, table, indexName).Scan(&n).Error
	return n > 0
}

func countDuplicateGroups(db *gorm.DB, sql string) (int64, error) {
	var n int64
	if err := db.Raw(sql).Scan(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
