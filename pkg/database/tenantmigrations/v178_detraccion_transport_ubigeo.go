package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// v178SaleDetraccion declara SOLO las columnas nuevas de tenant_sale_detraccion (no usa el struct
// vivo para que la migración no cambie si el struct crece después).
type v178SaleDetraccion struct {
	SaleID                   uint     `gorm:"primaryKey"`
	OriginUbigeo             *string  `gorm:"column:origin_ubigeo;size:6"`
	OriginAddress            *string  `gorm:"column:origin_address;size:200"`
	DestinationUbigeo        *string  `gorm:"column:destination_ubigeo;size:6"`
	DestinationAddress       *string  `gorm:"column:destination_address;size:200"`
	ValorRefCargaEfectivaPen *float64 `gorm:"column:valor_ref_carga_efectiva_pen;type:decimal(15,2)"`
	ValorRefCargaUtilPen     *float64 `gorm:"column:valor_ref_carga_util_pen;type:decimal(15,2)"`
}

func (v178SaleDetraccion) TableName() string { return "tenant_sale_detraccion" }

// V178DetraccionTransportUbigeo agrega a tenant_sale_detraccion el ubigeo (cat. 13, 6 dígitos) y la
// dirección detallada del punto de origen y del destino, y los valores referenciales (PEN) sobre la
// carga efectiva y la carga útil nominal. SUNAT los exige en el XML de la operación 1004 (errores
// 3116-3126). Solo aditiva: columnas NULL; las ventas ya guardadas (solo texto libre en
// punto_origen/punto_destino) quedan intactas. Idempotente.
type V178DetraccionTransportUbigeo struct{}

func (V178DetraccionTransportUbigeo) Version() int { return 178 }
func (V178DetraccionTransportUbigeo) Name() string { return "detraccion_transport_ubigeo" }

func (V178DetraccionTransportUbigeo) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v178SaleDetraccion{}) {
		return nil
	}
	for _, field := range []string{
		"OriginUbigeo", "OriginAddress", "DestinationUbigeo", "DestinationAddress",
		"ValorRefCargaEfectivaPen", "ValorRefCargaUtilPen",
	} {
		if mig.HasColumn(&v178SaleDetraccion{}, field) {
			continue
		}
		if err := mig.AddColumn(&v178SaleDetraccion{}, field); err != nil {
			return fmt.Errorf("add tenant_sale_detraccion.%s: %w", field, err)
		}
	}
	return nil
}
