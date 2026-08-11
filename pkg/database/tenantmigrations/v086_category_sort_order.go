package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v086Category struct {
	ID        uint `gorm:"primaryKey"`
	SortOrder int  `gorm:"column:sort_order;default:0"`
}

func (v086Category) TableName() string { return "tenant_categories" }

// V086CategorySortOrder agrega sort_order a tenant_categories (orden en POS y panel).
type V086CategorySortOrder struct{}

func (V086CategorySortOrder) Version() int { return 86 }
func (V086CategorySortOrder) Name() string { return "category_sort_order" }

func (V086CategorySortOrder) Up(db *gorm.DB) error {
	mig := db.Migrator()
	c := &v086Category{}
	if !mig.HasTable(c) {
		return nil
	}
	if !mig.HasColumn(c, "SortOrder") {
		if err := mig.AddColumn(c, "SortOrder"); err != nil {
			return fmt.Errorf("add tenant_categories.sort_order: %w", err)
		}
	}
	return db.Exec("UPDATE tenant_categories SET sort_order = id WHERE sort_order = 0 OR sort_order IS NULL").Error
}
