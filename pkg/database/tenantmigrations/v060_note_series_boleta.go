package tenantmigrations

import (
	"fmt"
	"log"

	"gorm.io/gorm"
)

// V060NoteSeriesBoleta agrega series BC01/BD01 (NC/ND sobre boletas) por sucursal si faltan.
type V060NoteSeriesBoleta struct{}

func (V060NoteSeriesBoleta) Version() int  { return 60 }
func (V060NoteSeriesBoleta) Name() string { return "note_series_boleta" }

func (V060NoteSeriesBoleta) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_document_series") || !db.Migrator().HasTable("tenant_branches") {
		return nil
	}

	type branchRow struct {
		ID uint
	}
	var branches []branchRow
	if err := db.Table("tenant_branches").Select("id").Find(&branches).Error; err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	for _, b := range branches {
		if err := ensureSeriesPrefix(db, b.ID, "nota_credito", "07", "NOTA DE CRÉDITO BOLETA", "BC01"); err != nil {
			return err
		}
		if err := ensureSeriesPrefix(db, b.ID, "nota_debito", "08", "NOTA DE DÉBITO BOLETA", "BD01"); err != nil {
			return err
		}
	}
	return nil
}

func ensureSeriesPrefix(db *gorm.DB, branchID uint, category, sunatCode, docType, series string) error {
	var count int64
	err := db.Raw(`
		SELECT COUNT(*) FROM tenant_document_series
		WHERE branch_id = ? AND category = ? AND active = 1
		  AND UPPER(series) LIKE ?
	`, branchID, category, stringsLikePrefix(series)).Scan(&count).Error
	if err != nil {
		return fmt.Errorf("count %s: %w", series, err)
	}
	if count > 0 {
		return nil
	}

	res := db.Exec(`
		INSERT INTO tenant_document_series
			(branch_id, doc_type, sunat_code, category, series, correlative, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 1, 1, NOW(3), NOW(3))
	`, branchID, docType, sunatCode, category, series)
	if res.Error != nil {
		return fmt.Errorf("insert %s branch %d: %w", series, branchID, res.Error)
	}
	if res.RowsAffected > 0 {
		log.Printf("[v060] tenant branch %d: serie %s (%s) creada", branchID, series, category)
	}
	return nil
}

func stringsLikePrefix(series string) string {
	if len(series) < 2 {
		return series + "%"
	}
	return series[:2] + "%"
}
