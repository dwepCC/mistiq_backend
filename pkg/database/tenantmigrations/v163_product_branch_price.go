package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V163ProductBranchPrice crea tenant_product_branch_prices: override del precio base de un
// producto "normal" (sin unidades de venta) para una sucursal puntual. La tabla nace vacía — sin
// ninguna fila, todo producto sigue resolviendo su precio exactamente igual que antes
// (TenantProduct.SalePrice). No toca TenantProduct, TenantProductSaleUnit ni
// TenantProductSaleUnitBranchPrice.
type V163ProductBranchPrice struct{}

func (V163ProductBranchPrice) Version() int { return 163 }
func (V163ProductBranchPrice) Name() string { return "product_branch_price" }

func (V163ProductBranchPrice) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantProductBranchPrice{}); err != nil {
		return fmt.Errorf("crear tenant_product_branch_prices: %w", err)
	}
	return nil
}
