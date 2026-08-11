package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v081BankMovement struct {
	ID           uint  `gorm:"primaryKey"`
	ReversalOfID *uint `gorm:"column:reversal_of_id;index"`
}

func (v081BankMovement) TableName() string { return "tenant_bank_movements" }

type v081CashMovement struct {
	ID           uint  `gorm:"primaryKey"`
	ReversalOfID *uint `gorm:"column:reversal_of_id;index"`
}

func (v081CashMovement) TableName() string { return "tenant_cash_movements" }

// V081FinancialMovementReversal vincula movimientos compensatorios con el movimiento original.
type V081FinancialMovementReversal struct{}

func (V081FinancialMovementReversal) Version() int { return 81 }
func (V081FinancialMovementReversal) Name() string { return "financial_movement_reversal" }

func (V081FinancialMovementReversal) Up(db *gorm.DB) error {
	mig := db.Migrator()
	bank := &v081BankMovement{}
	if mig.HasTable(bank) && !mig.HasColumn(bank, "ReversalOfID") {
		if err := mig.AddColumn(bank, "ReversalOfID"); err != nil {
			return fmt.Errorf("add tenant_bank_movements.reversal_of_id: %w", err)
		}
	}
	cash := &v081CashMovement{}
	if mig.HasTable(cash) && !mig.HasColumn(cash, "ReversalOfID") {
		if err := mig.AddColumn(cash, "ReversalOfID"); err != nil {
			return fmt.Errorf("add tenant_cash_movements.reversal_of_id: %w", err)
		}
	}
	return nil
}
