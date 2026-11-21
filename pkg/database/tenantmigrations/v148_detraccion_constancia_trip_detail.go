package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V148DetraccionConstanciaTripDetail agrega a tenant_sale_detraccion dos campos que el sistema
// anterior (facturador-tukifac) sí pedía y Tukifac no tenía: "N° Constancia de pago - detracción"
// (opcional, 1001 y 1004) y "Detalle del viaje" (obligatorio en 1004). Ninguno de los dos viaja al
// XML SUNAT — son referencia interna/impresión, igual que en el sistema anterior.
type V148DetraccionConstanciaTripDetail struct{}

func (V148DetraccionConstanciaTripDetail) Version() int { return 148 }
func (V148DetraccionConstanciaTripDetail) Name() string {
	return "detraccion_constancia_trip_detail"
}

func (V148DetraccionConstanciaTripDetail) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantSaleDetraccion{}); err != nil {
		return fmt.Errorf("tenant sale detraccion constancia/trip_detail migrate: %w", err)
	}
	return nil
}
