package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V084InventoryOperationTransfer agrega tipo TRANSFER al catálogo (Tabla 12) en tenants existentes.
type V084InventoryOperationTransfer struct{}

func (V084InventoryOperationTransfer) Version() int { return 84 }
func (V084InventoryOperationTransfer) Name() string { return "inventory_operation_transfer" }

func (V084InventoryOperationTransfer) Up(db *gorm.DB) error {
	return database.SeedInventoryOperationTypes(db)
}
