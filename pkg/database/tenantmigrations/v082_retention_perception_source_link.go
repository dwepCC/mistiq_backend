package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V082RetentionPerceptionSourceLink vincula CRE/CPE con compra o venta origen (trazabilidad ERP).
type V082RetentionPerceptionSourceLink struct{}

func (V082RetentionPerceptionSourceLink) Version() int { return 82 }
func (V082RetentionPerceptionSourceLink) Name() string { return "retention_perception_source_link" }

type v082RetentionPurchaseID struct {
	PurchaseID *uint `gorm:"column:purchase_id;index"`
}

func (v082RetentionPurchaseID) TableName() string { return "tenant_retentions" }

type v082PerceptionSourceSaleID struct {
	SourceSaleID *uint `gorm:"column:source_sale_id;index"`
}

func (v082PerceptionSourceSaleID) TableName() string { return "tenant_perceptions" }

func (V082RetentionPerceptionSourceLink) Up(db *gorm.DB) error {
	if db.Migrator().HasTable("tenant_retentions") {
		st := &v082RetentionPurchaseID{}
		if !db.Migrator().HasColumn(st, "PurchaseID") {
			if err := db.Migrator().AddColumn(st, "PurchaseID"); err != nil {
				return fmt.Errorf("tenant_retentions.purchase_id: %w", err)
			}
		}
	}
	if db.Migrator().HasTable("tenant_perceptions") {
		st := &v082PerceptionSourceSaleID{}
		if !db.Migrator().HasColumn(st, "SourceSaleID") {
			if err := db.Migrator().AddColumn(st, "SourceSaleID"); err != nil {
				return fmt.Errorf("tenant_perceptions.source_sale_id: %w", err)
			}
		}
	}
	return nil
}
