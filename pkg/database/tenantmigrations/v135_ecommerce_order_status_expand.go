package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V135EcommerceOrderStatusExpand remapea el enum plano histórico de TenantEcommerceOrder.Status
// (nuevo|atendido|cerrado|cancelado) al ciclo de vida granular del Contrato ecommerce v2 §3.1
// (docs/ECOMMERCE-EVOLUTION-CONTRACT.md): PENDIENTE|CONFIRMADO|EN_PREPARACION|EMPAQUETADO|
// LISTO_PARA_DESPACHO|DESPACHADO|ENTREGADO|CANCELADO|RECHAZADO|DEVUELTO.
//
// Mapeo (aproximación documentada — no hay forma de saber si un pedido "atendido" histórico ya
// estaba empacado o no; ver riesgo #1 del contrato):
//   nuevo     -> PENDIENTE       (todavía no lo tocó nadie del staff)
//   atendido  -> EN_PREPARACION  (el staff ya lo tomó; es el estado intermedio más cercano)
//   cerrado   -> ENTREGADO       ("cerrado" significaba "ya quedó resuelto", con o sin
//                                  conversión a venta — ENTREGADO es la aproximación más fiel)
//   cancelado -> CANCELADO       (mapeo directo, sin ambigüedad)
//
// Todas las columnas de longitud 20 ya alcanzan para el valor más largo del enum nuevo
// (LISTO_PARA_DESPACHO, 19 caracteres) — no hace falta ALTER de tamaño de columna.
type V135EcommerceOrderStatusExpand struct{}

func (V135EcommerceOrderStatusExpand) Version() int { return 135 }
func (V135EcommerceOrderStatusExpand) Name() string { return "ecommerce_order_status_expand" }

var v135StatusMap = map[string]string{
	"nuevo":     "PENDIENTE",
	"atendido":  "EN_PREPARACION",
	"cerrado":   "ENTREGADO",
	"cancelado": "CANCELADO",
}

func (V135EcommerceOrderStatusExpand) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_ecommerce_orders") {
		return nil
	}
	for oldVal, newVal := range v135StatusMap {
		if err := db.Table("tenant_ecommerce_orders").
			Where("status = ?", oldVal).
			Update("status", newVal).Error; err != nil {
			return fmt.Errorf("v135 remap status %s->%s: %w", oldVal, newVal, err)
		}
	}
	// El default de columna solo importa como resguardo (CreateOrder siempre fija Status a mano);
	// se actualiza igual para que quede consistente con el struct tag nuevo.
	if err := db.Exec("ALTER TABLE tenant_ecommerce_orders ALTER COLUMN status SET DEFAULT 'PENDIENTE'").Error; err != nil {
		return fmt.Errorf("v135 alter default status: %w", err)
	}
	return nil
}
