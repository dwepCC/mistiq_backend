package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v157Branch struct {
	ID      uint   `gorm:"primaryKey"`
	LogoURL string `gorm:"column:logo_url;type:longtext"`
}

func (v157Branch) TableName() string { return "tenant_branches" }

// V157BranchLogo agrega logo_url a tenant_branches: logo propio opcional por sucursal. Vacío =
// se sigue usando el logo global de TenantCompanyConfig (ver pkg/branchlogo.ResolveURL) — mismo
// patrón override+fallback que ya usa TenantProductSaleUnitBranchPrice para precios por sucursal.
//
// Puramente aditiva: columna nullable/vacía, ningún tenant pierde el comportamiento actual
// (logo único global) hasta que configure explícitamente un logo por sucursal.
type V157BranchLogo struct{}

func (V157BranchLogo) Version() int { return 157 }
func (V157BranchLogo) Name() string { return "branch_logo" }

func (V157BranchLogo) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v157Branch{}) {
		return nil
	}
	if !mig.HasColumn(&v157Branch{}, "LogoURL") {
		if err := mig.AddColumn(&v157Branch{}, "LogoURL"); err != nil {
			return fmt.Errorf("add tenant_branches.logo_url: %w", err)
		}
	}
	return nil
}
