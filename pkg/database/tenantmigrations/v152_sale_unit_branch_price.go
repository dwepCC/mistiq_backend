package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V152SaleUnitBranchPrice crea tenant_product_sale_unit_branch_prices: precio de una
// TenantProductSaleUnit específico para una sucursal (Fase 4). La tabla nace vacía — sin ninguna
// fila, toda SaleUnit sigue resolviendo su precio global exactamente igual que antes (ver
// pkg/saleunit.ResolvePrice). No toca TenantProductSaleUnit, TenantProduct, TenantProductStock,
// TenantStockMovement ni TenantProductPresentation.
type V152SaleUnitBranchPrice struct{}

func (V152SaleUnitBranchPrice) Version() int { return 152 }
func (V152SaleUnitBranchPrice) Name() string { return "sale_unit_branch_price" }

func (V152SaleUnitBranchPrice) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantProductSaleUnitBranchPrice{}); err != nil {
		return fmt.Errorf("crear tenant_product_sale_unit_branch_prices: %w", err)
	}
	return nil
}
