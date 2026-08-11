package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V090InventorySeriesPerBranch backfill ING001/EGR001 por sucursal y retira UNIQUE global
// que impedía repetir esos códigos internos de almacén en cada sucursal.
type V090InventorySeriesPerBranch struct{}

func (V090InventorySeriesPerBranch) Version() int { return 90 }
func (V090InventorySeriesPerBranch) Name() string { return "inventory_series_per_branch" }

func (V090InventorySeriesPerBranch) Up(db *gorm.DB) error {
	if db.Migrator().HasTable("tenant_document_series") {
		if migrationHasIndex(db, "tenant_document_series", idxDocumentSeriesCode) {
			if err := db.Exec(fmt.Sprintf(`DROP INDEX %s ON tenant_document_series`, idxDocumentSeriesCode)).Error; err != nil {
				return fmt.Errorf("drop %s: %w", idxDocumentSeriesCode, err)
			}
		}
	}
	return database.SeedInventoryDocumentSeriesForAllBranches(db)
}
