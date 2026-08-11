package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v056Presentation struct {
	ID        uint
	ProductID uint
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (v056Presentation) TableName() string { return "tenant_product_presentations" }

// V056ProductPresentationsSoftDelete agrega deleted_at a tenant_product_presentations.
// v055 creó la tabla sin soft delete; GORM filtra deleted_at IS NULL en cada consulta.
type V056ProductPresentationsSoftDelete struct{}

func (V056ProductPresentationsSoftDelete) Version() int { return 56 }
func (V056ProductPresentationsSoftDelete) Name() string { return "product_presentations_soft_delete" }

func (V056ProductPresentationsSoftDelete) Up(db *gorm.DB) error {
	mig := db.Migrator()
	pres := &v056Presentation{}
	if !mig.HasTable(pres) {
		return nil
	}
	if !mig.HasColumn(pres, "DeletedAt") {
		if err := mig.AddColumn(pres, "DeletedAt"); err != nil {
			return fmt.Errorf("tenant_product_presentations.deleted_at: %w", err)
		}
	}
	return nil
}
