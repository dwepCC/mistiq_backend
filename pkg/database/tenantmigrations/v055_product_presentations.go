package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v055Presentation struct {
	ID        uint    `gorm:"primaryKey"`
	ProductID uint    `gorm:"not null;index"`
	Name      string  `gorm:"size:120;not null"`
	SalePrice float64 `gorm:"type:decimal(15,2);not null"`
	SortOrder int     `gorm:"default:0"`
	Active    bool    `gorm:"default:true"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (v055Presentation) TableName() string { return "tenant_product_presentations" }

type v055ModifierGroup struct {
	ID          uint
	Kind        string
	Required    bool
	MultiSelect bool
}

func (v055ModifierGroup) TableName() string { return "tenant_modifier_groups" }

type v055ModifierOption struct {
	ID         uint
	GroupID    uint
	Name       string
	ExtraPrice float64
	Active     bool
}

func (v055ModifierOption) TableName() string { return "tenant_modifier_options" }

type v055ProductModifierLink struct {
	ProductID uint
	GroupID   uint
}

func (v055ProductModifierLink) TableName() string { return "tenant_product_modifier_groups" }

// V055ProductPresentations: presentaciones por producto (separadas de grupos globales de extras).
type V055ProductPresentations struct{}

func (V055ProductPresentations) Version() int { return 55 }
func (V055ProductPresentations) Name() string { return "product_presentations" }

func (V055ProductPresentations) Up(db *gorm.DB) error {
	mig := db.Migrator()
	pres := &v055Presentation{}
	if !mig.HasTable(pres) {
		if err := mig.CreateTable(pres); err != nil {
			return fmt.Errorf("tenant_product_presentations: %w", err)
		}
	}

	if !mig.HasTable(&v055ModifierGroup{}) {
		return nil
	}

	var presentationGroupIDs []uint
	if err := db.Model(&v055ModifierGroup{}).
		Where("kind = ? OR (required = ? AND multi_select = ?)", "presentation", true, false).
		Pluck("id", &presentationGroupIDs).Error; err != nil {
		return err
	}
	if len(presentationGroupIDs) == 0 {
		return nil
	}

	var links []v055ProductModifierLink
	if err := db.Where("group_id IN ?", presentationGroupIDs).Find(&links).Error; err != nil {
		return err
	}

	now := time.Now()
	sortByProduct := map[uint]int{}
	for _, link := range links {
		var opts []v055ModifierOption
		if err := db.Where("group_id = ? AND active = ?", link.GroupID, true).Order("id ASC").Find(&opts).Error; err != nil {
			return err
		}
		for _, opt := range opts {
			name := opt.Name
			if name == "" {
				continue
			}
			row := v055Presentation{
				ProductID: link.ProductID,
				Name:      name,
				SalePrice: opt.ExtraPrice,
				SortOrder: sortByProduct[link.ProductID],
				Active:    true,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := db.Create(&row).Error; err != nil {
				return err
			}
			sortByProduct[link.ProductID]++
		}
	}

	if err := db.Where("group_id IN ?", presentationGroupIDs).Delete(&v055ProductModifierLink{}).Error; err != nil {
		return err
	}
	if err := db.Model(&v055ModifierGroup{}).Where("id IN ?", presentationGroupIDs).Update("active", false).Error; err != nil {
		return err
	}

	productIDs := make([]uint, 0, len(sortByProduct))
	for pid := range sortByProduct {
		productIDs = append(productIDs, pid)
	}
	if len(productIDs) > 0 {
		if err := db.Table("tenant_products").Where("id IN ?", productIDs).Update("has_variants", true).Error; err != nil {
			return err
		}
	}

	return db.Model(&v055ModifierGroup{}).Where("kind = ?", "presentation").Update("kind", "extra").Error
}
