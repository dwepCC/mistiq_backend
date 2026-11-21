package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v166Quotation struct {
	ID                   uint    `gorm:"primaryKey"`
	GlobalDiscountMode   string  `gorm:"column:global_discount_mode;size:20"`
	GlobalDiscountValue  float64 `gorm:"column:global_discount_value;type:decimal(15,4);default:0"`
	GlobalDiscountAmount float64 `gorm:"column:global_discount_amount;type:decimal(15,2);default:0"`
}

func (v166Quotation) TableName() string { return "tenant_quotations" }

type v166QuotationItem struct {
	ID                uint    `gorm:"primaryKey"`
	LineDiscountMode  string  `gorm:"column:line_discount_mode;size:20"`
	LineDiscountValue float64 `gorm:"column:line_discount_value;type:decimal(15,4);default:0"`
}

func (v166QuotationItem) TableName() string { return "tenant_quotation_items" }

// V166QuotationDiscountModes guarda en la cotización el descuento tal como lo tecleó el usuario
// (modo percent|amount + valor, por línea y global) y repara price_includes_igv.
//
// Problema (revisión de cotizaciones, 05-oct-2026): la cotización solo guardaba el monto de
// descuento por línea (`discount`). Sin el modo ni el global, abrirla para editar o convertirla
// desde el formulario de venta mostraba el descuento en 0 y el total pasaba, en silencio, de
// S/ 18 a S/ 20; el "15 %" global ni siquiera tenía dónde vivir.
//
// Aditiva: columnas nuevas nullable/con default 0. Las cotizaciones anteriores quedan con modo
// vacío y siguen funcionando por el camino legado (usan `discount`).
//
// Backfill de price_includes_igv: `default:true` en el modelo hacía que GORM reemplazara un false
// explícito por true al insertar, así que toda cotización con precios SIN IGV quedó marcada como
// "IGV incluido". Solo se corrige cuando los importes guardados lo prueban sin ambigüedad: en una
// línea gravada con precio sin IGV, (cantidad × precio − descuento) coincide con el SUBTOTAL y no
// con el TOTAL; con IGV incluido ocurre al revés. Las filas dudosas no se tocan.
type V166QuotationDiscountModes struct{}

func (V166QuotationDiscountModes) Version() int { return 166 }
func (V166QuotationDiscountModes) Name() string { return "quotation_discount_modes" }

func (V166QuotationDiscountModes) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if mig.HasTable("tenant_quotations") {
		for _, col := range []string{"GlobalDiscountMode", "GlobalDiscountValue", "GlobalDiscountAmount"} {
			if !mig.HasColumn(&v166Quotation{}, col) {
				if err := mig.AddColumn(&v166Quotation{}, col); err != nil {
					return fmt.Errorf("add tenant_quotations.%s: %w", col, err)
				}
			}
		}
	}
	if mig.HasTable("tenant_quotation_items") {
		for _, col := range []string{"LineDiscountMode", "LineDiscountValue"} {
			if !mig.HasColumn(&v166QuotationItem{}, col) {
				if err := mig.AddColumn(&v166QuotationItem{}, col); err != nil {
					return fmt.Errorf("add tenant_quotation_items.%s: %w", col, err)
				}
			}
		}
		if err := db.Exec(`
			UPDATE tenant_quotation_items
			SET price_includes_igv = 0
			WHERE price_includes_igv = 1
			  AND igv_affectation_type IN ('10','11','12','13','14','15','16','17')
			  AND ABS(quantity * unit_price - discount - subtotal) <= 0.02
			  AND ABS(quantity * unit_price - discount - total) > 0.02
			  AND tax_amount > 0.02
		`).Error; err != nil {
			return fmt.Errorf("backfill tenant_quotation_items.price_includes_igv: %w", err)
		}
	}
	return nil
}
