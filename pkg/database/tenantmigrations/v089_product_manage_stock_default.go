package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v089ProductManageStock struct {
	ManageStock bool `gorm:"column:manage_stock;default:false"`
}

func (v089ProductManageStock) TableName() string { return "tenant_products" }

// V089ProductManageStockDefault alinea manage_stock: por defecto no controla inventario (solo si el usuario lo activa).
type V089ProductManageStockDefault struct{}

func (V089ProductManageStockDefault) Version() int { return 89 }
func (V089ProductManageStockDefault) Name() string { return "product_manage_stock_default_false" }

func (V089ProductManageStockDefault) Up(db *gorm.DB) error {
	mig := db.Migrator()
	p := &v089ProductManageStock{}
	if !mig.HasTable(p) {
		return nil
	}
	if !mig.HasColumn(p, "ManageStock") {
		return nil
	}
	switch db.Dialector.Name() {
	case "mysql":
		if err := db.Exec(
			`ALTER TABLE tenant_products MODIFY COLUMN manage_stock TINYINT(1) NOT NULL DEFAULT 0`,
		).Error; err != nil {
			return fmt.Errorf("tenant_products.manage_stock default: %w", err)
		}
	default:
		// SQLite/tests: el valor se persiste vía servicio; el default de columna no es crítico.
	}
	return nil
}
