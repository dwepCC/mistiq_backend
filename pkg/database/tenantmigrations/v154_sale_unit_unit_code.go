package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v154SaleUnit struct {
	ID     uint   `gorm:"primaryKey"`
	UnitID *uint  `gorm:"column:unit_id;index"`
	Unit   string `gorm:"column:unit;size:50"`
}

func (v154SaleUnit) TableName() string { return "tenant_product_sale_units" }

// V154SaleUnitUnitCode agrega unit_id/unit a tenant_product_sale_units: la unidad comercial SUNAT
// (Catálogo N°03) propia de cada SaleUnit (ej. "Caja" → BX), siguiendo exactamente el mismo patrón
// ya usado en TenantProduct (UnitID → TenantUnit, Unit denormalizado desde TenantUnit.Code — ver
// ProductService.resolveUnitReference). Antes de esta migración, una línea de venta con SaleUnit
// terminaba con TenantSaleItem.Unit = unidad BASE del producto (ej. "NIU") en vez del código real
// de la unidad vendida ("BX") — ver FRONTEND_PHASE_7E1_UNIT_PROPAGATION_AUDIT.md.
//
// Puramente aditiva: columnas nullable/vacías, nada existente cambia de significado ni de valor.
// Las SaleUnits creadas ANTES de esta migración quedan con unit_id=NULL/unit="" — el backend ya
// contempla ese caso como resguardo (usa la unidad base del producto, nunca infiere un código a
// partir del nombre de la SaleUnit — ver resolveSaleItemUnitCode en
// internal/sales/service/sale_service_calc.go). Un administrador que quiera que una SaleUnit
// existente declare su propio código SUNAT debe volver a guardarla desde el editor de catálogo
// (Fase 7B, SaleUnitsEditor.tsx) una vez ahí el selector de unidad esté disponible.
type V154SaleUnitUnitCode struct{}

func (V154SaleUnitUnitCode) Version() int { return 154 }
func (V154SaleUnitUnitCode) Name() string { return "sale_unit_unit_code" }

func (V154SaleUnitUnitCode) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v154SaleUnit{}) {
		return nil
	}
	if !mig.HasColumn(&v154SaleUnit{}, "UnitID") {
		if err := mig.AddColumn(&v154SaleUnit{}, "UnitID"); err != nil {
			return fmt.Errorf("add tenant_product_sale_units.unit_id: %w", err)
		}
	}
	if !mig.HasColumn(&v154SaleUnit{}, "Unit") {
		if err := mig.AddColumn(&v154SaleUnit{}, "Unit"); err != nil {
			return fmt.Errorf("add tenant_product_sale_units.unit: %w", err)
		}
	}
	return nil
}
