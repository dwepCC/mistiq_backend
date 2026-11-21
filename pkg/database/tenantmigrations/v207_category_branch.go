package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v207Category struct {
	BranchID *uint `gorm:"column:branch_id;index"`
}

func (v207Category) TableName() string { return "tenant_categories" }

// V207CategoryBranch agrega tenant_categories.branch_id: la sucursal a la que pertenece la categoría. NULL = todas las
// sucursales, así que NO hay backfill: toda categoría existente (y cualquiera que un código antiguo cree sin indicar sucursal)
// sigue visible en todas, como hasta hoy. Solo agrega una columna nullable con índice; no toca ninguna fila. Idempotente.
type V207CategoryBranch struct{}

func (V207CategoryBranch) Version() int { return 207 }
func (V207CategoryBranch) Name() string { return "category_branch" }

func (V207CategoryBranch) Up(db *gorm.DB) error {
	mig := db.Migrator()
	cat := &v207Category{}
	if !mig.HasColumn(cat, "BranchID") {
		if err := mig.AddColumn(cat, "BranchID"); err != nil {
			return fmt.Errorf("add tenant_categories.branch_id: %w", err)
		}
	}
	if !mig.HasIndex(cat, "idx_tenant_categories_branch_id") {
		if err := mig.CreateIndex(cat, "BranchID"); err != nil {
			return fmt.Errorf("index tenant_categories.branch_id: %w", err)
		}
	}
	return nil
}
