package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v088PreparationArea struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (v088PreparationArea) TableName() string { return "tenant_preparation_areas" }

// V088PreparationAreasTimestamps alinea tenant_preparation_areas con TenantPreparationArea (timestamps + soft delete).
// v087 creó la tabla sin deleted_at; GORM filtra deleted_at IS NULL y falla en tenants ya migrados.
type V088PreparationAreasTimestamps struct{}

func (V088PreparationAreasTimestamps) Version() int { return 88 }
func (V088PreparationAreasTimestamps) Name() string { return "preparation_areas_timestamps" }

func (V088PreparationAreasTimestamps) Up(db *gorm.DB) error {
	mig := db.Migrator()
	area := &v088PreparationArea{}
	if !mig.HasTable(area) {
		return nil
	}
	for _, field := range []string{"CreatedAt", "UpdatedAt", "DeletedAt"} {
		if !mig.HasColumn(area, field) {
			if err := mig.AddColumn(area, field); err != nil {
				return fmt.Errorf("tenant_preparation_areas.%s: %w", field, err)
			}
		}
	}
	return nil
}
