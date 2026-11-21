package tenantmigrations

import (
	"gorm.io/gorm"
)

// V196PurchaseExchangeRate agrega tenant_purchases.exchange_rate (TC venta de las compras en USD).
// Compras existentes: moneda PEN y TC NULL — sin backfill. Idempotente.
type V196PurchaseExchangeRate struct{}

func (V196PurchaseExchangeRate) Version() int { return 196 }
func (V196PurchaseExchangeRate) Name() string { return "purchase_exchange_rate" }

func (V196PurchaseExchangeRate) Up(db *gorm.DB) error {
	if db.Migrator().HasColumn(&v195Purchase{}, "exchange_rate") {
		return nil
	}
	return db.Migrator().AddColumn(&v195Purchase{}, "ExchangeRate")
}
