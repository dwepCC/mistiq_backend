package tenantmigrations

import "gorm.io/gorm"

// V181POSCostAndPricesPermissions agrega pos.view_cost y pos.view_prices (botones «Costo» y
// «Precios» de las tarjetas del POS). V180 se amplió con ellos después de haberse aplicado en
// algunas bases, así que esta versión vuelve a correr su Up, que es idempotente (FirstOrCreate de
// los permisos y grantIfMissing por rol), para completar lo que faltaba.
type V181POSCostAndPricesPermissions struct{}

func (V181POSCostAndPricesPermissions) Version() int { return 181 }
func (V181POSCostAndPricesPermissions) Name() string { return "pos_cost_and_prices_permissions" }

func (V181POSCostAndPricesPermissions) Up(db *gorm.DB) error {
	return V180POSStockAndProductCostPermissions{}.Up(db)
}
