package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v061Product struct {
	ID           uint  `gorm:"primaryKey"`
	IsRestaurant bool  `gorm:"column:is_restaurant"`
	BranchID     *uint `gorm:"column:branch_id;index"`
}

func (v061Product) TableName() string { return "tenant_products" }

// V061ProductBranchID catálogo de restaurante por sucursal (branch_id en producto).
type V061ProductBranchID struct{}

func (V061ProductBranchID) Version() int { return 61 }
func (V061ProductBranchID) Name() string { return "product_branch_id" }

func (V061ProductBranchID) Up(db *gorm.DB) error {
	mig := db.Migrator()
	p := &v061Product{}
	if !mig.HasTable(p) {
		return nil
	}
	if !mig.HasColumn(p, "BranchID") {
		if err := mig.AddColumn(p, "BranchID"); err != nil {
			return fmt.Errorf("add tenant_products.branch_id: %w", err)
		}
	}

	mainID, err := v061MainBranchID(db)
	if err != nil {
		return err
	}

	// Platos con stock: sucursal = primera fila de stock (MIN branch_id).
	if err := db.Exec(`
		UPDATE tenant_products p
		INNER JOIN (
			SELECT product_id, MIN(branch_id) AS branch_id
			FROM tenant_product_stocks
			GROUP BY product_id
		) s ON s.product_id = p.id
		SET p.branch_id = s.branch_id
		WHERE p.is_restaurant = 1 AND (p.branch_id IS NULL OR p.branch_id = 0)
	`).Error; err != nil {
		return fmt.Errorf("backfill product branch from stock: %w", err)
	}

	// Resto de platos sin stock: sucursal principal.
	if err := db.Exec(`
		UPDATE tenant_products
		SET branch_id = ?
		WHERE is_restaurant = 1 AND (branch_id IS NULL OR branch_id = 0)
	`, mainID).Error; err != nil {
		return fmt.Errorf("backfill product branch main: %w", err)
	}

	return nil
}

func v061MainBranchID(db *gorm.DB) (uint, error) {
	var b struct {
		ID uint
	}
	if err := db.Table("tenant_branches").Where("is_main = ? AND active = ?", true, true).First(&b).Error; err == nil && b.ID > 0 {
		return b.ID, nil
	}
	if err := db.Table("tenant_branches").Where("active = ?", true).Order("id ASC").First(&b).Error; err == nil && b.ID > 0 {
		return b.ID, nil
	}
	return 1, nil
}
