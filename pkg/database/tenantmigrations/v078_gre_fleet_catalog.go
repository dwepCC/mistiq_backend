package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v078GreCarrier struct {
	ID            uint   `gorm:"primaryKey"`
	DocType       string `gorm:"size:2;not null;default:'6'"`
	DocNumber     string `gorm:"size:20;not null;index"`
	BusinessName  string `gorm:"size:255;not null"`
	FiscalAddress string `gorm:"size:500"`
	MTCNumber     string `gorm:"size:50"`
	IsDefault     bool   `gorm:"default:false;index"`
	Active        bool   `gorm:"default:true;index"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (v078GreCarrier) TableName() string { return "tenant_gre_carriers" }

type v078GreDriver struct {
	ID            uint   `gorm:"primaryKey"`
	DocType       string `gorm:"size:2;not null;default:'1'"`
	DocNumber     string `gorm:"size:20;not null;index"`
	FullName      string `gorm:"size:255;not null"`
	LicenseNumber string `gorm:"size:50"`
	Phone         string `gorm:"size:30"`
	CarrierID     *uint  `gorm:"index"`
	IsDefault     bool   `gorm:"default:false;index"`
	Active        bool   `gorm:"default:true;index"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (v078GreDriver) TableName() string { return "tenant_gre_drivers" }

type v078GreVehicle struct {
	ID               uint   `gorm:"primaryKey"`
	Plate            string `gorm:"size:20;not null;uniqueIndex"`
	Brand            string `gorm:"size:80"`
	Model            string `gorm:"size:80"`
	HabilitationCert string `gorm:"size:100"`
	CarrierID        *uint  `gorm:"index"`
	IsDefault        bool   `gorm:"default:false;index"`
	Active           bool   `gorm:"default:true;index"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (v078GreVehicle) TableName() string { return "tenant_gre_vehicles" }

// V078GreFleetCatalog catálogos GRE: transportistas, conductores y vehículos.
type V078GreFleetCatalog struct{}

func (V078GreFleetCatalog) Version() int { return 78 }
func (V078GreFleetCatalog) Name() string { return "gre_fleet_catalog" }

func (V078GreFleetCatalog) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&v078GreCarrier{}, &v078GreDriver{}, &v078GreVehicle{}); err != nil {
		return fmt.Errorf("gre fleet catalog: %w", err)
	}
	return nil
}
