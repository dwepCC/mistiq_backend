package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V083InventoryIngressEgress tablas ingreso/egreso, catálogo de operaciones y series de almacén.
type V083InventoryIngressEgress struct{}

func (V083InventoryIngressEgress) Version() int { return 83 }
func (V083InventoryIngressEgress) Name() string { return "inventory_ingress_egress" }

func (V083InventoryIngressEgress) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&database.TenantInventoryOperationType{},
		&database.TenantInventoryDocument{},
		&database.TenantInventoryDocumentDetail{},
		&database.TenantStockMovement{},
	); err != nil {
		return err
	}
	if err := database.SeedInventoryOperationTypes(db); err != nil {
		return err
	}
	return database.SeedInventoryDocumentSeriesForAllBranches(db)
}
