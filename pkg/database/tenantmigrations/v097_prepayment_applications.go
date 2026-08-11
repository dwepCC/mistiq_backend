package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V097PrepaymentApplications tabla de aplicaciones de anticipos (Fase 1 deducción).
type V097PrepaymentApplications struct{}

func (V097PrepaymentApplications) Version() int { return 97 }
func (V097PrepaymentApplications) Name() string { return "prepayment_applications" }

func (V097PrepaymentApplications) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantSalePrepaymentApplication{}); err != nil {
		return fmt.Errorf("tenant prepayment applications v97 migrate: %w", err)
	}
	return nil
}
