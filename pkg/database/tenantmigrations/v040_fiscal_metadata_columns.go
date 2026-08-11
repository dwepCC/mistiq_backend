package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// V040FiscalMetadataColumns añade metadatos fiscales en tenant_company_configs (SSOT en facturador).
type V040FiscalMetadataColumns struct{}

func (V040FiscalMetadataColumns) Version() int { return 40 }
func (V040FiscalMetadataColumns) Name() string { return "fiscal_metadata_columns" }

type v040CompanyConfigFiscal struct {
	SunatEnabled           bool       `gorm:"column:sunat_enabled;default:false"`
	SunatEnvMode           string     `gorm:"column:sunat_env_mode;size:20;default:demo"`
	SendMode               string     `gorm:"column:send_mode;size:30;default:sunat_direct"`
	FiscalProvider         string     `gorm:"column:fiscal_provider;size:50"`
	FiscalConnectionType   string     `gorm:"column:fiscal_connection_type;size:20;default:bearer"`
	FiscalConnectionStatus string     `gorm:"column:fiscal_connection_status;size:30"`
	FiscalLastSyncAt       *time.Time `gorm:"column:fiscal_last_sync_at"`
	SunatConnected         bool       `gorm:"column:sunat_connected;default:false"`
}

func (v040CompanyConfigFiscal) TableName() string { return "tenant_company_configs" }

func (V040FiscalMetadataColumns) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_company_configs") {
		return nil
	}
	st := &v040CompanyConfigFiscal{}
	mig := db.Migrator()
	cols := []string{
		"SunatEnabled",
		"SunatEnvMode",
		"SendMode",
		"FiscalProvider",
		"FiscalConnectionType",
		"FiscalConnectionStatus",
		"FiscalLastSyncAt",
		"SunatConnected",
	}
	for _, col := range cols {
		if !mig.HasColumn(st, col) {
			if err := mig.AddColumn(st, col); err != nil {
				return fmt.Errorf("tenant_company_configs.%s: %w", col, err)
			}
		}
	}
	return nil
}
