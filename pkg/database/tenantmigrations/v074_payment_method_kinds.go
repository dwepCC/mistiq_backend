package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V074PaymentMethodKinds repara catálogo financiero idempotente (sin columna kind legacy).
type V074PaymentMethodKinds struct{}

func (V074PaymentMethodKinds) Version() int { return 74 }
func (V074PaymentMethodKinds) Name() string { return "payment_method_kinds" }

func (V074PaymentMethodKinds) Up(db *gorm.DB) error {
	mig := db.Migrator()
	pm := &database.TenantPaymentMethod{}
	if !mig.HasTable(pm) {
		return nil
	}
	// Tablas de condiciones/tributarios (idempotente; necesarias antes del seed).
	if err := db.AutoMigrate(&database.TenantPaymentCondition{}, &database.TenantTaxPaymentType{}); err != nil {
		return fmt.Errorf("ensure financial catalog tables: %w", err)
	}
	if err := database.SeedFinancialCatalog(db); err != nil {
		return fmt.Errorf("seed financial catalog: %w", err)
	}
	return nil
}
