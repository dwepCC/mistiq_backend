package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V185PurchaseItemFractionalUnitCost amplía tenant_purchase_items.unit_cost de DECIMAL(15,2) a
// DECIMAL(15,6): una compra de 20000 g a S/ 0.003 el gramo guardaba el costo como 0.00 en silencio
// (el subtotal y el stock sí salían bien porque se calculan con el valor sin redondear, pero el
// detalle de la compra mostraba un costo unitario falso). Mismo problema y misma solución que V184
// para las ventas. Aditiva e idempotente: MODIFY COLUMN a un tipo más ancho no pierde datos.
type V185PurchaseItemFractionalUnitCost struct{}

func (V185PurchaseItemFractionalUnitCost) Version() int { return 185 }
func (V185PurchaseItemFractionalUnitCost) Name() string { return "purchase_item_fractional_unit_cost" }

func (V185PurchaseItemFractionalUnitCost) Up(db *gorm.DB) error {
	if db.Dialector.Name() != "mysql" {
		return nil
	}
	mig := db.Migrator()
	if !mig.HasTable("tenant_purchase_items") || !mig.HasColumn("tenant_purchase_items", "unit_cost") {
		return nil
	}
	if err := db.Exec("ALTER TABLE tenant_purchase_items MODIFY COLUMN unit_cost DECIMAL(15,6) NOT NULL").Error; err != nil {
		return fmt.Errorf("tenant_purchase_items.unit_cost: %w", err)
	}
	return nil
}
