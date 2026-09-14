package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V126SaleDetraccionTransporte extiende tenant_sale_detraccion para soportar la operación 1004
// (detracción por transporte de carga por vía terrestre, Resolución 073-2006-SUNAT). Agrega
// operation_type_code (para distinguir 1001 de 1004 al armar el payload del facturador) y los
// campos de transporte que SUNAT exige como AdditionalItemProperty (catálogo 55) en el ítem:
// valor referencial, registro MTC, configuración vehicular, origen/destino y carga efectiva/útil.
type V126SaleDetraccionTransporte struct{}

func (V126SaleDetraccionTransporte) Version() int { return 126 }
func (V126SaleDetraccionTransporte) Name() string { return "sale_detraccion_transporte" }

func (V126SaleDetraccionTransporte) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantSaleDetraccion{}); err != nil {
		return fmt.Errorf("tenant sale detraccion transporte migrate: %w", err)
	}
	return nil
}
