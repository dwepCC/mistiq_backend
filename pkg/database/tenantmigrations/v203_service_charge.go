package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// Structs mínimos: solo las columnas nuevas (evita un AutoMigrate del struct completo, que emitiría ALTER ... MODIFY sobre tablas grandes).
type v203CompanyConfig struct {
	ID                   uint    `gorm:"primaryKey"`
	ServiceChargeEnabled bool    `gorm:"column:service_charge_enabled;default:false"`
	ServiceChargeRate    float64 `gorm:"column:service_charge_rate;type:decimal(5,2);default:10"`
}

func (v203CompanyConfig) TableName() string { return "tenant_company_configs" }

type v203Sale struct {
	ID                  uint    `gorm:"primaryKey"`
	ServiceChargeRate   float64 `gorm:"column:service_charge_rate;type:decimal(5,2);default:0"`
	ServiceChargeAmount float64 `gorm:"column:service_charge_amount;type:decimal(15,2);default:0"`
}

func (v203Sale) TableName() string { return "tenant_sales" }

// V203ServiceCharge agrega el recargo al consumo: interruptor y porcentaje por empresa, y tasa/monto cobrados en cada venta. Solo agrega
// columnas con default (0 / apagado): las ventas existentes quedan idénticas y no hace falta backfill. Idempotente.
type V203ServiceCharge struct{}

func (V203ServiceCharge) Version() int { return 203 }
func (V203ServiceCharge) Name() string { return "service_charge" }

func (V203ServiceCharge) Up(db *gorm.DB) error {
	mig := db.Migrator()
	cfg := &v203CompanyConfig{}
	for _, f := range []string{"ServiceChargeEnabled", "ServiceChargeRate"} {
		if !mig.HasColumn(cfg, f) {
			if err := mig.AddColumn(cfg, f); err != nil {
				return fmt.Errorf("add tenant_company_configs.%s: %w", f, err)
			}
		}
	}
	sale := &v203Sale{}
	for _, f := range []string{"ServiceChargeRate", "ServiceChargeAmount"} {
		if !mig.HasColumn(sale, f) {
			if err := mig.AddColumn(sale, f); err != nil {
				return fmt.Errorf("add tenant_sales.%s: %w", f, err)
			}
		}
	}
	return nil
}
