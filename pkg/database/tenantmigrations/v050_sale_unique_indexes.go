package tenantmigrations

import (
	"fmt"
	"log"

	"gorm.io/gorm"
)

// V050SaleUniqueIndexes: UNIQUE(series_id, correlative) en ventas.
// No usa UNIQUE(branch_id, number): el número completo puede repetirse entre series distintas
// y la regla SUNAT exige unicidad del código de serie (ver v052).
type V050SaleUniqueIndexes struct{}

func (V050SaleUniqueIndexes) Version() int { return 50 }
func (V050SaleUniqueIndexes) Name() string { return "sale_unique_indexes" }

const idxSaleSeriesCorrelative = "uk_tenant_sales_series_correlative"

func (V050SaleUniqueIndexes) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_sales") {
		return nil
	}

	dupSC, err := countDuplicateGroups(db, `
		SELECT COUNT(*) FROM (
			SELECT series_id, correlative
			FROM tenant_sales
			WHERE deleted_at IS NULL
			GROUP BY series_id, correlative
			HAVING COUNT(*) > 1
		) dup`)
	if err != nil {
		return fmt.Errorf("auditoría (series_id, correlative): %w", err)
	}

	if dupSC > 0 {
		log.Printf("[v050] tenant: omitiendo UNIQUE(series_id,correlative) — duplicados activos=%d (scripts/audit_sale_number_uniques.sql)", dupSC)
		logSaleDuplicateSamples(db)
		return nil
	}

	if !migrationHasIndex(db, "tenant_sales", idxSaleSeriesCorrelative) {
		if err := db.Exec(fmt.Sprintf(
			`CREATE UNIQUE INDEX %s ON tenant_sales (series_id, correlative)`,
			idxSaleSeriesCorrelative,
		)).Error; err != nil {
			return fmt.Errorf("crear %s: %w", idxSaleSeriesCorrelative, err)
		}
	}

	return nil
}

func logSaleDuplicateSamples(db *gorm.DB) {
	var sc []struct {
		SeriesID    uint
		Correlative uint
		Cantidad    int64
		SaleIDs     string
	}
	_ = db.Raw(`
		SELECT series_id, correlative, COUNT(*) AS cantidad, GROUP_CONCAT(id ORDER BY id) AS sale_ids
		FROM tenant_sales WHERE deleted_at IS NULL
		GROUP BY series_id, correlative HAVING COUNT(*) > 1 LIMIT 5
	`).Scan(&sc).Error
	for _, r := range sc {
		log.Printf("[v050] dup series_id=%d correlative=%d count=%d ids=%s", r.SeriesID, r.Correlative, r.Cantidad, r.SaleIDs)
	}
}
