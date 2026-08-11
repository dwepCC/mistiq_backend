package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v109Comanda struct {
	ID             uint  `gorm:"primaryKey"`
	PresentationID *uint `gorm:"column:presentation_id;index"`
}

func (v109Comanda) TableName() string { return "tenant_comandas" }

// V109ComandaPresentation agrega presentation_id a las comandas de restaurante (Tukichef), para
// que facturar una mesa descuente el stock de la variante correcta y no el agregado del producto
// — mismo fix que ya se hizo para ventas de catálogo (Tukifac POS/SalesRegister).
type V109ComandaPresentation struct{}

func (V109ComandaPresentation) Version() int { return 109 }
func (V109ComandaPresentation) Name() string { return "comanda_presentation" }

func (V109ComandaPresentation) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v109Comanda{}) {
		return nil
	}
	if mig.HasColumn(&v109Comanda{}, "PresentationID") {
		return nil
	}
	if err := mig.AddColumn(&v109Comanda{}, "PresentationID"); err != nil {
		return fmt.Errorf("add tenant_comandas.presentation_id: %w", err)
	}
	return nil
}
