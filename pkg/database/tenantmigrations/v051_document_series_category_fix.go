package tenantmigrations

import (
	"fmt"
	"log"

	"gorm.io/gorm"
)

// V051DocumentSeriesCategoryFix alinea category con sunat_code 07/08 (NC/ND).
type V051DocumentSeriesCategoryFix struct{}

func (V051DocumentSeriesCategoryFix) Version() int { return 51 }
func (V051DocumentSeriesCategoryFix) Name() string { return "document_series_category_fix" }

func (V051DocumentSeriesCategoryFix) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_document_series") {
		return nil
	}

	var auditNC, auditND int64
	_ = db.Raw(`SELECT COUNT(*) FROM tenant_document_series WHERE TRIM(sunat_code) = '07' AND category <> 'nota_credito'`).Scan(&auditNC).Error
	_ = db.Raw(`SELECT COUNT(*) FROM tenant_document_series WHERE TRIM(sunat_code) = '08' AND category <> 'nota_debito'`).Scan(&auditND).Error
	if auditNC > 0 || auditND > 0 {
		log.Printf("[v051] tenant: corrigiendo categorías — NC(07)=%d ND(08)=%d", auditNC, auditND)
	}

	res := db.Exec(`
		UPDATE tenant_document_series
		SET category = 'nota_credito', updated_at = NOW(3)
		WHERE TRIM(sunat_code) = '07' AND category <> 'nota_credito'
	`)
	if res.Error != nil {
		return fmt.Errorf("nota_credito: %w", res.Error)
	}
	if res.RowsAffected > 0 {
		log.Printf("[v051] tenant: %d filas → category=nota_credito (sunat 07)", res.RowsAffected)
	}

	res = db.Exec(`
		UPDATE tenant_document_series
		SET category = 'nota_debito', updated_at = NOW(3)
		WHERE TRIM(sunat_code) = '08' AND category <> 'nota_debito'
	`)
	if res.Error != nil {
		return fmt.Errorf("nota_debito: %w", res.Error)
	}
	if res.RowsAffected > 0 {
		log.Printf("[v051] tenant: %d filas → category=nota_debito (sunat 08)", res.RowsAffected)
	}

	return nil
}
