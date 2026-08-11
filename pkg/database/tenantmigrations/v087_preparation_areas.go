package tenantmigrations

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type v087PreparationArea struct {
	ID        uint   `gorm:"primaryKey"`
	Name      string `gorm:"size:100;not null"`
	Slug      string `gorm:"size:50;not null;uniqueIndex"`
	SortOrder int    `gorm:"column:sort_order;default:0"`
	Active    bool   `gorm:"default:true"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (v087PreparationArea) TableName() string { return "tenant_preparation_areas" }

type v087Product struct {
	ID                uint   `gorm:"primaryKey"`
	IsRestaurant      bool   `gorm:"column:is_restaurant"`
	PreparationArea   string `gorm:"column:preparation_area;size:50"`
	PreparationAreaID *uint  `gorm:"column:preparation_area_id"`
}

func (v087Product) TableName() string { return "tenant_products" }

var v087DefaultPreparationAreas = []struct {
	Name string
	Slug string
	Ord  int
}{
	{"Cocina", "cocina", 1},
	{"Bar", "bar", 2},
	{"Barra", "barra", 3},
	{"Postres", "postres", 4},
	{"Otro", "otro", 5},
}

// V087PreparationAreas crea tenant_preparation_areas, vincula productos existentes por slug.
type V087PreparationAreas struct{}

func (V087PreparationAreas) Version() int { return 87 }
func (V087PreparationAreas) Name() string { return "preparation_areas" }

func (V087PreparationAreas) Up(db *gorm.DB) error {
	mig := db.Migrator()
	areaModel := &v087PreparationArea{}
	if err := mig.AutoMigrate(areaModel); err != nil {
		return fmt.Errorf("auto migrate tenant_preparation_areas: %w", err)
	}

	productModel := &v087Product{}
	if !mig.HasTable(productModel) {
		return nil
	}
	if !mig.HasColumn(productModel, "PreparationAreaID") {
		if err := mig.AddColumn(productModel, "PreparationAreaID"); err != nil {
			return fmt.Errorf("add tenant_products.preparation_area_id: %w", err)
		}
	}

	var areaCount int64
	if err := db.Model(areaModel).Count(&areaCount).Error; err != nil {
		return err
	}
	if areaCount == 0 {
		for _, row := range v087DefaultPreparationAreas {
			if err := db.Create(&v087PreparationArea{
				Name: row.Name, Slug: row.Slug, SortOrder: row.Ord, Active: true,
			}).Error; err != nil {
				return fmt.Errorf("seed preparation area %q: %w", row.Slug, err)
			}
		}
	}

	type slugRow struct {
		Slug string
	}
	var unknown []slugRow
	if err := db.Raw(`
		SELECT DISTINCT LOWER(TRIM(preparation_area)) AS slug
		FROM tenant_products
		WHERE is_restaurant = 1
		  AND TRIM(preparation_area) <> ''
		  AND LOWER(TRIM(preparation_area)) NOT IN (SELECT slug FROM tenant_preparation_areas)
	`).Scan(&unknown).Error; err != nil {
		return fmt.Errorf("list unknown preparation areas: %w", err)
	}
	var maxOrder int
	if err := db.Model(areaModel).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder).Error; err != nil {
		return err
	}
	for _, row := range unknown {
		slug := strings.TrimSpace(strings.ToLower(row.Slug))
		if slug == "" {
			continue
		}
		maxOrder++
		name := slug
		if len(name) > 0 {
			name = strings.ToUpper(name[:1]) + name[1:]
		}
		if err := db.Create(&v087PreparationArea{
			Name: name, Slug: slug, SortOrder: maxOrder, Active: true,
		}).Error; err != nil {
			return fmt.Errorf("create preparation area %q: %w", slug, err)
		}
	}

	if err := db.Exec(`
		UPDATE tenant_products p
		INNER JOIN tenant_preparation_areas a ON a.slug = LOWER(TRIM(p.preparation_area))
		SET p.preparation_area_id = a.id
		WHERE p.is_restaurant = 1
		  AND TRIM(p.preparation_area) <> ''
		  AND (p.preparation_area_id IS NULL OR p.preparation_area_id = 0)
	`).Error; err != nil {
		return fmt.Errorf("link products to preparation areas: %w", err)
	}

	return db.Exec(`
		UPDATE tenant_products p
		INNER JOIN tenant_preparation_areas a ON a.id = p.preparation_area_id
		SET p.preparation_area = a.slug
		WHERE p.is_restaurant = 1
		  AND p.preparation_area_id IS NOT NULL
		  AND p.preparation_area_id > 0
	`).Error
}
