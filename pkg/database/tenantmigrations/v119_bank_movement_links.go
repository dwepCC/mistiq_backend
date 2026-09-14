package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v119BankMovement struct {
	SaleID     *uint `gorm:"column:sale_id;index"`
	PurchaseID *uint `gorm:"column:purchase_id;index"`
}

func (v119BankMovement) TableName() string { return "tenant_bank_movements" }

// V119BankMovementLinks agrega sale_id/purchase_id tipados a tenant_bank_movements — antes solo
// existía "reference" (texto libre) para vincular un movimiento a su venta/compra de origen,
// igual que ya tiene tenant_cash_movements. Habilita reversiones/devoluciones parciales robustas
// (no dependen de que el texto de referencia coincida exacto) y futuras funciones de UI (badge de
// origen, clic-para-ver-venta) sin parsear "reference".
type V119BankMovementLinks struct{}

func (V119BankMovementLinks) Version() int { return 119 }
func (V119BankMovementLinks) Name() string { return "bank_movement_links" }

func (V119BankMovementLinks) Up(db *gorm.DB) error {
	mig := db.Migrator()
	row := &v119BankMovement{}
	if !mig.HasColumn(row, "SaleID") {
		if err := mig.AddColumn(row, "SaleID"); err != nil {
			return fmt.Errorf("add tenant_bank_movements.sale_id: %w", err)
		}
	}
	if !mig.HasColumn(row, "PurchaseID") {
		if err := mig.AddColumn(row, "PurchaseID"); err != nil {
			return fmt.Errorf("add tenant_bank_movements.purchase_id: %w", err)
		}
	}
	return nil
}
