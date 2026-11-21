package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v177ProductBranch struct {
	ID        uint `gorm:"primaryKey"`
	ProductID uint `gorm:"not null;uniqueIndex:idx_product_branch"`
	BranchID  uint `gorm:"not null;uniqueIndex:idx_product_branch;index"`
	CreatedAt time.Time
}

func (v177ProductBranch) TableName() string { return "tenant_product_branches" }

// V177ProductBranches crea tenant_product_branches: en qué sucursales se vende cada producto. Sin
// filas = todas las sucursales (comportamiento previo), así que no hace falta backfill. Solo
// aditiva; idempotente.
type V177ProductBranches struct{}

func (V177ProductBranches) Version() int { return 177 }
func (V177ProductBranches) Name() string { return "product_branches" }

func (V177ProductBranches) Up(db *gorm.DB) error {
	if db.Migrator().HasTable(&v177ProductBranch{}) {
		return nil
	}
	if err := db.Migrator().CreateTable(&v177ProductBranch{}); err != nil {
		return fmt.Errorf("create tenant_product_branches: %w", err)
	}
	return nil
}
