package tenantmigrations

import "gorm.io/gorm"

// V171RepairHasVariants vuelve a marcar has_variants en los productos que tienen presentaciones
// activas pero la bandera apagada.
//
// Antes, editar un producto desde un formulario que no enviaba la lista de presentaciones (carta del
// restaurante, panel de combos) ponía has_variants=false aunque las filas siguieran existiendo, y el
// POS / Nuevo comprobante dejaban de pedir qué presentación vender. Idempotente.
type V171RepairHasVariants struct{}

func (V171RepairHasVariants) Version() int { return 171 }
func (V171RepairHasVariants) Name() string { return "repair_has_variants" }

func (V171RepairHasVariants) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable("tenant_products") || !mig.HasTable("tenant_product_presentations") {
		return nil
	}
	return db.Exec(`UPDATE tenant_products SET has_variants = ?
		WHERE has_variants = ? AND deleted_at IS NULL
		AND id IN (SELECT product_id FROM tenant_product_presentations WHERE active = ? AND deleted_at IS NULL)`,
		true, false, true).Error
}
