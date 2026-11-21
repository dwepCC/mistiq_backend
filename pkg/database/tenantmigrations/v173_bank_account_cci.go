package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v173BankAccount struct {
	ID  uint   `gorm:"primaryKey"`
	CCI string `gorm:"column:cci;size:40"`
}

func (v173BankAccount) TableName() string { return "tenant_bank_accounts" }

// V173BankAccountCCI agrega tenant_bank_accounts.cci: el Código de Cuenta Interbancario (20
// dígitos) de la cuenta, que se imprime junto al número de cuenta en los comprobantes y
// cotizaciones. Columna nullable sin backfill; idempotente.
type V173BankAccountCCI struct{}

func (V173BankAccountCCI) Version() int { return 173 }
func (V173BankAccountCCI) Name() string { return "bank_account_cci" }

func (V173BankAccountCCI) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v173BankAccount{}) || mig.HasColumn(&v173BankAccount{}, "CCI") {
		return nil
	}
	if err := mig.AddColumn(&v173BankAccount{}, "CCI"); err != nil {
		return fmt.Errorf("add tenant_bank_accounts.cci: %w", err)
	}
	return nil
}
