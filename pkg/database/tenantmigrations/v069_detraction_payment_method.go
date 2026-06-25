package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V069DetractionPaymentMethod garantiza detraccion_bn en tenant_tax_payment_types (SPOT).
type V069DetractionPaymentMethod struct{}

func (V069DetractionPaymentMethod) Version() int  { return 69 }
func (V069DetractionPaymentMethod) Name() string { return "detraction_payment_method" }

func (V069DetractionPaymentMethod) Up(db *gorm.DB) error {
	if err := database.EnsureDetractionTaxPaymentType(db); err != nil {
		return fmt.Errorf("tenant detraction payment method: %w", err)
	}
	return nil
}
