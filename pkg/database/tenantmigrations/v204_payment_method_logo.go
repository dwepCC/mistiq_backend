package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// Struct mínimo: solo la columna nueva (evita un AutoMigrate del struct completo).
type v204PaymentMethod struct {
	ID      uint   `gorm:"primaryKey"`
	LogoURL string `gorm:"column:logo_url;type:text"`
}

func (v204PaymentMethod) TableName() string { return "tenant_payment_methods" }

// V204PaymentMethodLogo agrega el logo propio de cada método de pago (data URL pequeño, ya reducido por el panel).
// Solo agrega una columna nullable: los métodos existentes siguen usando el icono por defecto. Idempotente.
type V204PaymentMethodLogo struct{}

func (V204PaymentMethodLogo) Version() int { return 204 }
func (V204PaymentMethodLogo) Name() string { return "payment_method_logo" }

func (V204PaymentMethodLogo) Up(db *gorm.DB) error {
	mig := db.Migrator()
	m := &v204PaymentMethod{}
	if !mig.HasColumn(m, "LogoURL") {
		if err := mig.AddColumn(m, "LogoURL"); err != nil {
			return fmt.Errorf("add tenant_payment_methods.logo_url: %w", err)
		}
	}
	return nil
}
