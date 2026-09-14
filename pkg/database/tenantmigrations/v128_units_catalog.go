package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type v128Product struct {
	ID     uint  `gorm:"primaryKey"`
	UnitID *uint `gorm:"column:unit_id;index"`
}

func (v128Product) TableName() string { return "tenant_products" }

// V128UnitsCatalog crea tenant_units (catálogo SUNAT N°03 gestionable desde Tukifac, visible en
// Tukifac/Tukichef) y agrega tenant_products.unit_id — hasta ahora "unidad de medida" era texto
// suelto sin catálogo propio detrás (ver pkg/sunat/units.go, que solo normaliza/valida, no
// persiste). Siembra el catálogo por defecto y vincula por ID los productos existentes según su
// Unit (texto) actual.
type V128UnitsCatalog struct{}

func (V128UnitsCatalog) Version() int { return 128 }
func (V128UnitsCatalog) Name() string { return "units_catalog" }

func (V128UnitsCatalog) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantUnit{}); err != nil {
		return fmt.Errorf("crear tenant_units: %w", err)
	}
	mig := db.Migrator()
	p := &v128Product{}
	if !mig.HasColumn(p, "UnitID") {
		if err := mig.AddColumn(p, "UnitID"); err != nil {
			return fmt.Errorf("add tenant_products.unit_id: %w", err)
		}
	}
	if err := database.SeedUnitsCatalog(db); err != nil {
		return fmt.Errorf("seed tenant_units: %w", err)
	}
	if err := database.BackfillProductUnitIDs(db); err != nil {
		return fmt.Errorf("backfill tenant_products.unit_id: %w", err)
	}
	return nil
}
