package tenantmigrations

import (
	"fmt"
	"log"

	"gorm.io/gorm"
)

// V062CreditNoteBCSeries agrega serie BC01 por sucursal si falta (NC para anular boletas).
type V062CreditNoteBCSeries struct{}

func (V062CreditNoteBCSeries) Version() int { return 62 }
func (V062CreditNoteBCSeries) Name() string {
	return "credit_note_bc_series"
}

func (V062CreditNoteBCSeries) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_document_series") || !db.Migrator().HasTable("tenant_branches") {
		return nil
	}
	res := db.Exec(`
		INSERT INTO tenant_document_series
			(branch_id, doc_type, sunat_code, category, series, correlative, active, created_at, updated_at)
		SELECT b.id, 'NOTA_CREDITO', '07', 'nota_credito', 'BC01', 1, 1, NOW(3), NOW(3)
		FROM tenant_branches b
		WHERE NOT EXISTS (
			SELECT 1 FROM tenant_document_series s
			WHERE s.branch_id = b.id
			  AND s.category = 'nota_credito'
			  AND UPPER(s.series) LIKE 'BC%'
		)
	`)
	if res.Error != nil {
		return fmt.Errorf("insert BC01: %w", res.Error)
	}
	if res.RowsAffected > 0 {
		log.Printf("[v062] tenant: %d sucursal(es) con nueva serie BC01 (nota de crédito boletas)", res.RowsAffected)
	}
	return nil
}
