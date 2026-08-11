package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V085InventoryDocumentSource agrega columna source en documentos y tipos de ajuste de inventario.
type V085InventoryDocumentSource struct{}

func (V085InventoryDocumentSource) Version() int { return 85 }
func (V085InventoryDocumentSource) Name() string { return "inventory_document_source" }

func (V085InventoryDocumentSource) Up(db *gorm.DB) error {
	st := &database.TenantInventoryDocument{}
	if db.Migrator().HasTable(st) && !db.Migrator().HasColumn(st, "Source") {
		if err := db.Migrator().AddColumn(st, "Source"); err != nil {
			return fmt.Errorf("tenant_inventory_documents.source: %w", err)
		}
	}
	return database.SeedInventoryOperationTypes(db)
}
