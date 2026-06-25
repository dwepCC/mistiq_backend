package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v063CompanyConfig struct {
	ReceiptBankAccountIDs string `gorm:"column:receipt_bank_account_ids;type:text"`
}

func (v063CompanyConfig) TableName() string { return "tenant_company_configs" }

// V063ReceiptBankAccountIDs cuentas bancarias visibles en ticket/comprobantes.
type V063ReceiptBankAccountIDs struct{}

func (V063ReceiptBankAccountIDs) Version() int { return 63 }
func (V063ReceiptBankAccountIDs) Name() string { return "receipt_bank_account_ids" }

func (V063ReceiptBankAccountIDs) Up(db *gorm.DB) error {
	mig := db.Migrator()
	cfg := &v063CompanyConfig{}
	if !mig.HasTable(cfg) {
		return nil
	}
	if !mig.HasColumn(cfg, "ReceiptBankAccountIDs") {
		if err := mig.AddColumn(cfg, "ReceiptBankAccountIDs"); err != nil {
			return fmt.Errorf("add tenant_company_configs.receipt_bank_account_ids: %w", err)
		}
	}
	return nil
}
