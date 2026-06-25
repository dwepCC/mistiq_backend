package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V067SaleCurrencyOperation moneda, tipo de cambio y tipo operación en cabecera de venta.
type V067SaleCurrencyOperation struct{}

func (V067SaleCurrencyOperation) Version() int  { return 67 }
func (V067SaleCurrencyOperation) Name() string { return "sale_currency_operation" }

func (V067SaleCurrencyOperation) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantSale{}); err != nil {
		return fmt.Errorf("tenant_sales currency/operation migrate: %w", err)
	}
	return nil
}
