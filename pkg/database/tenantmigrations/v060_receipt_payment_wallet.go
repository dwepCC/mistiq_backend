package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v060CompanyConfig struct {
	WalletProvider     string `gorm:"column:wallet_provider;size:20"`
	WalletPhone        string `gorm:"column:wallet_phone;size:30"`
	WalletQrURL        string `gorm:"column:wallet_qr_url;type:longtext"`
	WalletShowOnA4     bool   `gorm:"column:wallet_show_on_a4;default:false"`
	WalletShowOnTicket bool   `gorm:"column:wallet_show_on_ticket;default:false"`
}

func (v060CompanyConfig) TableName() string { return "tenant_company_configs" }

// V060ReceiptPaymentWallet QR Yape/Plin opcional en comprobantes locales.
type V060ReceiptPaymentWallet struct{}

func (V060ReceiptPaymentWallet) Version() int { return 60 }
func (V060ReceiptPaymentWallet) Name() string { return "receipt_payment_wallet" }

func (V060ReceiptPaymentWallet) Up(db *gorm.DB) error {
	mig := db.Migrator()
	cfg := &v060CompanyConfig{}
	if !mig.HasTable(cfg) {
		return nil
	}
	cols := []string{"WalletProvider", "WalletPhone", "WalletQrURL", "WalletShowOnA4", "WalletShowOnTicket"}
	for _, col := range cols {
		if !mig.HasColumn(cfg, col) {
			if err := mig.AddColumn(cfg, col); err != nil {
				return fmt.Errorf("add tenant_company_configs.%s: %w", col, err)
			}
		}
	}
	return nil
}
