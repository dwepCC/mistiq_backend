package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v182EcommerceSettings struct {
	ID                 uint   `gorm:"primaryKey"`
	PaymentMethodsJSON string `gorm:"column:payment_methods_json;type:text"`
}

func (v182EcommerceSettings) TableName() string { return "tenant_ecommerce_settings" }

// V182EcommercePaymentMethods agrega payment_methods_json: los medios de pago (Yape, Plin, cuentas
// bancarias, efectivo…) que el tenant muestra al cliente al hacer el pedido en la tienda virtual.
// Solo informativo: la tienda no cobra ni valida pagos. Idempotente.
type V182EcommercePaymentMethods struct{}

func (V182EcommercePaymentMethods) Version() int { return 182 }
func (V182EcommercePaymentMethods) Name() string { return "ecommerce_payment_methods" }

func (V182EcommercePaymentMethods) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v182EcommerceSettings{}) {
		return nil
	}
	if mig.HasColumn(&v182EcommerceSettings{}, "PaymentMethodsJSON") {
		return nil
	}
	if err := mig.AddColumn(&v182EcommerceSettings{}, "PaymentMethodsJSON"); err != nil {
		return fmt.Errorf("add tenant_ecommerce_settings.payment_methods_json: %w", err)
	}
	return nil
}
