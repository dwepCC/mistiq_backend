package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V184SaleUnitFractionalPrices amplía a DECIMAL(15,6) los precios que antes eran DECIMAL(15,2) y
// que ahora pueden valer menos de un céntimo por unidad: un gramo de arroz a S/ 4.50 el kilo cuesta
// S/ 0.0045, que con 2 decimales se guardaba en silencio como 0.00. Cubre las unidades de venta
// (price1/2/3), su override por sucursal y el precio unitario de las líneas de cotización (las de
// venta ya eran DECIMAL(15,6) desde V047). El total de cada línea sigue redondeándose a 2/6
// decimales en el motor de cálculo; solo el precio unitario gana precisión.
//
// También habilita en el catálogo del tenant las unidades GRM (gramo) y MLT (mililitro), que venían
// inactivas, para poder elegirlas como unidad de venta fraccionada. Aditiva e idempotente: MODIFY
// COLUMN a un tipo más ancho no pierde datos y volver a correrla no cambia nada.
type V184SaleUnitFractionalPrices struct{}

func (V184SaleUnitFractionalPrices) Version() int { return 184 }
func (V184SaleUnitFractionalPrices) Name() string { return "sale_unit_fractional_prices" }

func (V184SaleUnitFractionalPrices) Up(db *gorm.DB) error {
	if db.Dialector.Name() != "mysql" {
		return nil
	}
	alters := []struct {
		table, column, definition string
	}{
		{"tenant_product_sale_units", "price1", "DECIMAL(15,6) NOT NULL"},
		{"tenant_product_sale_units", "price2", "DECIMAL(15,6) NULL"},
		{"tenant_product_sale_units", "price3", "DECIMAL(15,6) NULL"},
		{"tenant_product_sale_unit_branch_prices", "price1", "DECIMAL(15,6) NOT NULL"},
		{"tenant_product_sale_unit_branch_prices", "price2", "DECIMAL(15,6) NULL"},
		{"tenant_product_sale_unit_branch_prices", "price3", "DECIMAL(15,6) NULL"},
		{"tenant_quotation_items", "unit_price", "DECIMAL(15,6) NOT NULL"},
	}
	mig := db.Migrator()
	for _, a := range alters {
		if !mig.HasTable(a.table) || !mig.HasColumn(a.table, a.column) {
			continue
		}
		sql := fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s %s", a.table, a.column, a.definition)
		if err := db.Exec(sql).Error; err != nil {
			return fmt.Errorf("%s.%s: %w", a.table, a.column, err)
		}
	}
	if mig.HasTable("tenant_units") && mig.HasColumn("tenant_units", "active") {
		if err := db.Exec("UPDATE tenant_units SET active = 1 WHERE code IN ('GRM','MLT') AND active = 0").Error; err != nil {
			return fmt.Errorf("activar unidades GRM/MLT: %w", err)
		}
	}
	return nil
}
