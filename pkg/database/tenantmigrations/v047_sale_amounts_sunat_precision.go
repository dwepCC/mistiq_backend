package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V047SaleAmountsSunatPrecision amplía montos de venta a DECIMAL(15,6) para cálculos SUNAT.
type V047SaleAmountsSunatPrecision struct{}

func (V047SaleAmountsSunatPrecision) Version() int { return 47 }
func (V047SaleAmountsSunatPrecision) Name() string { return "sale_amounts_sunat_precision" }

func (V047SaleAmountsSunatPrecision) Up(db *gorm.DB) error {
	alters := []struct {
		table  string
		column string
	}{
		{"tenant_sales", "subtotal"},
		{"tenant_sales", "tax_amount"},
		{"tenant_sales", "total"},
		{"tenant_sale_items", "unit_price"},
		{"tenant_sale_items", "discount"},
		{"tenant_sale_items", "subtotal"},
		{"tenant_sale_items", "tax_amount"},
		{"tenant_sale_items", "total"},
		{"tenant_sale_payments", "amount"},
		{"tenant_table_sessions", "total_amount"},
	}
	for _, a := range alters {
		if !db.Migrator().HasTable(a.table) || !db.Migrator().HasColumn(a.table, a.column) {
			continue
		}
		sql := fmt.Sprintf(
			"ALTER TABLE %s MODIFY COLUMN %s DECIMAL(15,6) NOT NULL",
			a.table, a.column,
		)
		if a.column == "discount" {
			sql = fmt.Sprintf(
				"ALTER TABLE %s MODIFY COLUMN %s DECIMAL(15,6) NOT NULL DEFAULT 0",
				a.table, a.column,
			)
		}
		if a.table == "tenant_table_sessions" && a.column == "total_amount" {
			sql = fmt.Sprintf(
				"ALTER TABLE %s MODIFY COLUMN %s DECIMAL(15,6) NOT NULL DEFAULT 0",
				a.table, a.column,
			)
		}
		if err := db.Exec(sql).Error; err != nil {
			return fmt.Errorf("%s.%s: %w", a.table, a.column, err)
		}
	}
	return nil
}
