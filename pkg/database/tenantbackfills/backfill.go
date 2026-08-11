package tenantbackfills

import "gorm.io/gorm"

// TenantBackfill migración de datos run-once (no DDL masivo en Up).
type TenantBackfill interface {
	Version() int
	Name() string
	Run(db *gorm.DB) error
}

// Described backfill que explica en castellano qué corrige.
//
// Opcional para no tocar los ya escritos. El panel central lo muestra al elegirlo: un nombre
// como «product_codes» no alcanza para decidir si ejecutarlo sobre datos de producción.
type Described interface {
	Description() string
}

// DescriptionOf texto del backfill, o vacío si no describe nada.
func DescriptionOf(b TenantBackfill) string {
	if d, ok := b.(Described); ok {
		return d.Description()
	}
	return ""
}
