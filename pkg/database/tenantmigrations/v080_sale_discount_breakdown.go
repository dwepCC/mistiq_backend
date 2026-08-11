package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v080Sale struct {
	ID                   uint    `gorm:"primaryKey"`
	GlobalDiscountAmount float64 `gorm:"column:global_discount_amount;type:decimal(15,2);default:0"`
	GlobalDiscountMode   string  `gorm:"column:global_discount_mode;size:20"`
	GlobalDiscountValue  float64 `gorm:"column:global_discount_value;type:decimal(15,2);default:0"`
}

func (v080Sale) TableName() string { return "tenant_sales" }

type v080SaleItem struct {
	ID                     uint    `gorm:"primaryKey"`
	LineDiscountSubtotal   float64 `gorm:"column:line_discount_subtotal;type:decimal(15,2);default:0"`
	GlobalDiscountSubtotal float64 `gorm:"column:global_discount_subtotal;type:decimal(15,2);default:0"`
}

func (v080SaleItem) TableName() string { return "tenant_sale_items" }

// V080SaleDiscountBreakdown agrega columnas de desglose de descuentos global/por línea.
type V080SaleDiscountBreakdown struct{}

func (V080SaleDiscountBreakdown) Version() int { return 80 }
func (V080SaleDiscountBreakdown) Name() string { return "sale_discount_breakdown" }

func (V080SaleDiscountBreakdown) Up(db *gorm.DB) error {
	mig := db.Migrator()
	sale := &v080Sale{}
	if mig.HasTable(sale) {
		if !mig.HasColumn(sale, "GlobalDiscountAmount") {
			if err := mig.AddColumn(sale, "GlobalDiscountAmount"); err != nil {
				return fmt.Errorf("add tenant_sales.global_discount_amount: %w", err)
			}
		}
		if !mig.HasColumn(sale, "GlobalDiscountMode") {
			if err := mig.AddColumn(sale, "GlobalDiscountMode"); err != nil {
				return fmt.Errorf("add tenant_sales.global_discount_mode: %w", err)
			}
		}
		if !mig.HasColumn(sale, "GlobalDiscountValue") {
			if err := mig.AddColumn(sale, "GlobalDiscountValue"); err != nil {
				return fmt.Errorf("add tenant_sales.global_discount_value: %w", err)
			}
		}
	}
	item := &v080SaleItem{}
	if mig.HasTable(item) {
		if !mig.HasColumn(item, "LineDiscountSubtotal") {
			if err := mig.AddColumn(item, "LineDiscountSubtotal"); err != nil {
				return fmt.Errorf("add tenant_sale_items.line_discount_subtotal: %w", err)
			}
		}
		if !mig.HasColumn(item, "GlobalDiscountSubtotal") {
			if err := mig.AddColumn(item, "GlobalDiscountSubtotal"); err != nil {
				return fmt.Errorf("add tenant_sale_items.global_discount_subtotal: %w", err)
			}
		}
	}
	return nil
}
